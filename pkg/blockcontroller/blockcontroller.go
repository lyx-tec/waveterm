// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package blockcontroller

import (
	"context"
	"encoding/base64"
	"fmt"
	"io/fs"
	"log"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wavetermdev/waveterm/pkg/blocklogger"
	"github.com/wavetermdev/waveterm/pkg/filestore"
	"github.com/wavetermdev/waveterm/pkg/jobcontroller"
	"github.com/wavetermdev/waveterm/pkg/remote"
	"github.com/wavetermdev/waveterm/pkg/remote/conncontroller"
	"github.com/wavetermdev/waveterm/pkg/sessiondaemon"
	"github.com/wavetermdev/waveterm/pkg/util/ds"
	"github.com/wavetermdev/waveterm/pkg/util/shellutil"
	"github.com/wavetermdev/waveterm/pkg/wavebase"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wps"
	"github.com/wavetermdev/waveterm/pkg/wshrpc/wshclient"
	"github.com/wavetermdev/waveterm/pkg/wslconn"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

const (
	BlockController_Shell   = "shell"
	BlockController_Cmd     = "cmd"
	BlockController_Tsunami = "tsunami"
)

const (
	Status_Running = "running"
	Status_Done    = "done"
	Status_Init    = "init"
)

const (
	DefaultTermMaxFileSize = 2 * 1024 * 1024
	DefaultHtmlMaxFileSize = 256 * 1024
	MaxInitScriptSize      = 50 * 1024
)

const DefaultTimeout = 2 * time.Second
const DefaultGracefulKillWait = 400 * time.Millisecond

type BlockInputUnion struct {
	InputData []byte            `json:"inputdata,omitempty"`
	SigName   string            `json:"signame,omitempty"`
	TermSize  *waveobj.TermSize `json:"termsize,omitempty"`
}

type BlockControllerRuntimeStatus struct {
	BlockId           string `json:"blockid"`
	Version           int64  `json:"version"`
	ShellProcStatus   string `json:"shellprocstatus,omitempty"`
	ShellProcConnName string `json:"shellprocconnname,omitempty"`
	ShellProcExitCode int    `json:"shellprocexitcode"`
	TsunamiPort       int    `json:"tsunamiport,omitempty"`
}

// Controller interface that all block controllers must implement
type Controller interface {
	Start(ctx context.Context, blockMeta waveobj.MetaMapType, rtOpts *waveobj.RuntimeOpts, force bool) error
	Stop(graceful bool, newStatus string, destroy bool)
	GetRuntimeStatus() *BlockControllerRuntimeStatus // does not return nil
	GetConnName() string
	SendInput(input *BlockInputUnion) error
}

// Registry for all controllers
var (
	controllerRegistry  = make(map[string]Controller)
	registryLock        sync.RWMutex
	blockResyncMutexMap = ds.MakeSyncMap[*sync.Mutex]()
)

func getBlockResyncMutex(blockId string) *sync.Mutex {
	return blockResyncMutexMap.GetOrCreate(blockId, func() *sync.Mutex {
		return &sync.Mutex{}
	})
}

// Registry operations
func getController(blockId string) Controller {
	registryLock.RLock()
	defer registryLock.RUnlock()
	return controllerRegistry[blockId]
}

func registerController(blockId string, controller Controller) {
	var existingController Controller

	registryLock.Lock()
	existing, exists := controllerRegistry[blockId]
	if exists {
		existingController = existing
	}
	controllerRegistry[blockId] = controller
	registryLock.Unlock()

	if existingController != nil {
		existingController.Stop(false, Status_Done, true)
		wstore.DeleteRTInfo(waveobj.MakeORef(waveobj.OType_Block, blockId))
	}
}

func deleteController(blockId string) {
	registryLock.Lock()
	defer registryLock.Unlock()
	delete(controllerRegistry, blockId)
}

func getAllControllers() map[string]Controller {
	registryLock.RLock()
	defer registryLock.RUnlock()
	// Return a copy to avoid lock issues
	result := make(map[string]Controller)
	for k, v := range controllerRegistry {
		result[k] = v
	}
	return result
}

func InitBlockController() {
	rpcClient := wshclient.GetBareRpcClient()
	rpcClient.EventListener.On(wps.Event_BlockClose, handleBlockCloseEvent)
	wshclient.EventSubCommand(rpcClient, wps.SubscriptionRequest{
		Event:     wps.Event_BlockClose,
		AllScopes: true,
	}, nil)
}

func handleBlockCloseEvent(event *wps.WaveEvent) {
	blockId, ok := event.Data.(string)
	if !ok {
		log.Printf("[blockclose] invalid event data type")
		return
	}
	go DestroyBlockController(blockId)
}

// Public API Functions

func ResyncController(ctx context.Context, tabId string, blockId string, rtOpts *waveobj.RuntimeOpts, force bool) error {
	if tabId == "" || blockId == "" {
		return fmt.Errorf("invalid tabId or blockId passed to ResyncController")
	}

	mu := getBlockResyncMutex(blockId)
	mu.Lock()
	defer mu.Unlock()

	blockData, err := wstore.DBMustGet[*waveobj.Block](ctx, blockId)
	if err != nil {
		return fmt.Errorf("error getting block: %w", err)
	}

	controllerName := blockData.Meta.GetString(waveobj.MetaKey_Controller, "")
	connName := blockData.Meta.GetString(waveobj.MetaKey_Connection, "")

	// Get existing controller
	existing := getController(blockId)

	// Check for connection change FIRST - always destroy on conn change
	if existing != nil {
		existingConnName := existing.GetConnName()
		if existingConnName != connName {
			// For non-local connections, check readiness before switching
			if !conncontroller.IsLocalConnName(connName) && !conncontroller.IsWslConnName(connName) && existingConnName == "" {
				err = CheckConnStatus(blockId)
				if err != nil {
					log.Printf("not stopping blockcontroller %s due to conn change (from %q to %q): new connection not ready\n", blockId, existingConnName, connName)
					return fmt.Errorf("cannot start shellproc: %w", err)
				}
			}
			log.Printf("stopping blockcontroller %s due to conn change (from %q to %q)\n", blockId, existingConnName, connName)
			stopBlockController(blockId)
			time.Sleep(100 * time.Millisecond)
			existing = nil
		}
	}

	// If no controller needed, stop existing if present
	if controllerName == "" {
		if existing != nil {
			DestroyBlockController(blockId)
		}
		return nil
	}

	// Check for SessionDaemon controller
	daemonId := blockData.Meta.GetString(waveobj.MetaKey_SessionDaemonId, "")

	// For local/WSL connections, session daemon is not applicable — clear and fall through to ShellController
	if daemonId != "" && controllerName == BlockController_Shell && (conncontroller.IsLocalConnName(connName) || conncontroller.IsWslConnName(connName)) {
		if existing != nil {
			DestroyBlockController(blockId)
			time.Sleep(100 * time.Millisecond)
			existing = nil
		}
		_ = wstore.DBUpdateFn(ctx, blockId, func(block *waveobj.Block) {
			delete(block.Meta, waveobj.MetaKey_SessionDaemonId)
		})
		daemonId = ""
	}

	// Validate existing daemon: if stale (done/not found), clear it
	if daemonId != "" && controllerName == BlockController_Shell {
		dbDaemon, err := wstore.DBMustGet[*waveobj.SessionDaemon](ctx, daemonId)
		if err != nil || dbDaemon.Status == sessiondaemon.Status_Done {
			log.Printf("[sessiondaemon] stale daemon=%s block=%s status=%s err=%v, clearing", daemonId, blockId, func() string { if dbDaemon != nil { return dbDaemon.Status }; return "db_load_error" }(), err)
			if existing != nil {
				DestroyBlockController(blockId)
				time.Sleep(100 * time.Millisecond)
				existing = nil
			}
			_ = wstore.DBUpdateFn(ctx, blockId, func(block *waveobj.Block) {
				delete(block.Meta, waveobj.MetaKey_SessionDaemonId)
			})
			daemonId = ""
		}
	}

	// Check if we need to morph controller type
	if existing != nil {
		needsReplace := false

		switch existing.(type) {
		case *ShellController:
			if daemonId != "" || (controllerName != BlockController_Shell && controllerName != BlockController_Cmd) {
				needsReplace = true
			}
		case *SessionDaemonController:
			sdc := existing.(*SessionDaemonController)
			if daemonId == "" || conncontroller.IsLocalConnName(connName) || conncontroller.IsWslConnName(connName) {
				needsReplace = true
			} else if daemonId != sdc.DaemonId {
				needsReplace = true
			}
		case *TsunamiController:
			if controllerName != BlockController_Tsunami {
				needsReplace = true
			}
		}

		if needsReplace {
			log.Printf("stopping blockcontroller %s due to controller type change\n", blockId)
			stopBlockController(blockId)
			time.Sleep(100 * time.Millisecond)
			existing = nil
		}
	}

	// Force restart if requested
	if force && existing != nil {
		status := existing.GetRuntimeStatus()
		if status.ShellProcStatus != Status_Running {
			stopBlockController(blockId)
			time.Sleep(100 * time.Millisecond)
			existing = nil
		}
	}

	// Destroy done controllers before restarting
	if existing != nil {
		status := existing.GetRuntimeStatus()
		if status.ShellProcStatus == Status_Done {
			log.Printf("destroying blockcontroller %s with done status before restart\n", blockId)
			stopBlockController(blockId)
			time.Sleep(100 * time.Millisecond)
			existing = nil
		}
	}

	// Create or restart controller
	var controller Controller
	if existing != nil {
		controller = existing
	} else {
		switch {
		case daemonId != "":
			sdc := MakeSessionDaemonController(tabId, blockId, connName)
			sdc.DaemonId = daemonId
			controller = sdc
			registerController(blockId, controller)
			// Ensure the daemon is in memory before attaching the block.
			// On restart, the daemon exists in DB but not in the in-memory
			// manager – AttachBlock silently no-ops if not found.
			dbDaemon, err := wstore.DBMustGet[*waveobj.SessionDaemon](ctx, daemonId)
			if err == nil {
				sessiondaemon.Manager.GetOrCreate(ctx, dbDaemon)
			}
			sessiondaemon.Manager.AttachBlock(ctx, daemonId, blockId)

		case controllerName == BlockController_Shell || controllerName == BlockController_Cmd:
			controller = MakeShellController(tabId, blockId, controllerName, connName)
			registerController(blockId, controller)

		case controllerName == BlockController_Tsunami:
			controller = MakeTsunamiController(tabId, blockId, connName)
			registerController(blockId, controller)

		default:
			return fmt.Errorf("unknown controller type %q", controllerName)
		}
	}

	// Check if we need to start/restart
	status := controller.GetRuntimeStatus()
	if status.ShellProcStatus == Status_Running {
		// For SessionDaemonController, verify the job is still alive.
		// The remote job manager may have died, leaving the daemon with a stale JobId.
		// If so, clear the JobId so Start() runs again on the next ResyncController call.
		if sdc, ok := controller.(*SessionDaemonController); ok {
			if daemon := sessiondaemon.Manager.Get(sdc.DaemonId); daemon != nil && daemon.JobId != "" {
				jobStatus, jErr := jobcontroller.GetJobManagerStatus(ctx, daemon.JobId)
				if jErr != nil || jobStatus != jobcontroller.JobManagerStatus_Running {
					log.Printf("[sessiondaemon] resync: job %s not running (status=%s err=%v), marking done", daemon.JobId, jobStatus, jErr)
					daemon.Lock.Lock()
					daemon.JobId = ""
					daemon.Lock.Unlock()
					wstore.DBUpdateFn(ctx, sdc.DaemonId, func(dbSd *waveobj.SessionDaemon) {
						dbSd.JobId = ""
						dbSd.Status = sessiondaemon.Status_Done
					})
				stopBlockController(blockId)
				time.Sleep(100 * time.Millisecond)
				existing = nil
				// Fall through to controller recreation + Start below
				}
			}
		}
	}
	if status.ShellProcStatus == Status_Init || existing == nil {
		// For shell/cmd, check connection status first (for non-local connections)
		if controllerName == BlockController_Shell || controllerName == BlockController_Cmd {
			if !conncontroller.IsLocalConnName(connName) {
				err = CheckConnStatus(blockId)
				if err != nil {
					return fmt.Errorf("cannot start shellproc: %w", err)
				}
			}
		}

		// Start controller
		err = controller.Start(ctx, blockData.Meta, rtOpts, force)
		if err != nil {
			return fmt.Errorf("error starting controller: %w", err)
		}
	}

	return nil
}

func GetBlockControllerRuntimeStatus(blockId string) *BlockControllerRuntimeStatus {
	controller := getController(blockId)
	if controller == nil {
		return nil
	}
	return controller.GetRuntimeStatus()
}

func stopBlockController(blockId string) {
	controller := getController(blockId)
	if controller == nil {
		return
	}
	stackBuf := make([]byte, 4096)
	stackLen := runtime.Stack(stackBuf, false)
	log.Printf("[sessiondaemon] stopBlockController: block=%s stack:\n%s", blockId, string(stackBuf[:stackLen]))
	controller.Stop(true, Status_Done, true)
	wstore.DeleteRTInfo(waveobj.MakeORef(waveobj.OType_Block, blockId))
}

func DestroyBlockController(blockId string) {
	stackBuf := make([]byte, 4096)
	stackLen := runtime.Stack(stackBuf, false)
	log.Printf("[sessiondaemon] DestroyBlockController: block=%s stack:\n%s", blockId, string(stackBuf[:stackLen]))
	stopBlockController(blockId)
	deleteController(blockId)
}

func sendConnMonitorInputNotification(controller Controller) {
	connName := controller.GetConnName()
	if connName == "" || conncontroller.IsLocalConnName(connName) || conncontroller.IsWslConnName(connName) {
		return
	}

	connOpts, parseErr := remote.ParseOpts(connName)
	if parseErr != nil {
		return
	}
	sshConn := conncontroller.MaybeGetConn(connOpts)
	if sshConn != nil {
		monitor := sshConn.GetMonitor()
		if monitor != nil {
			monitor.NotifyInput()
		}
	}
}

func SendInput(blockId string, inputUnion *BlockInputUnion) error {
	controller := getController(blockId)
	if controller == nil {
		return fmt.Errorf("no controller found for block %s", blockId)
	}
	sendConnMonitorInputNotification(controller)
	return controller.SendInput(inputUnion)
}

// only call this on shutdown
func StopAllBlockControllersForShutdown() {
	controllers := getAllControllers()
	for blockId, controller := range controllers {
		status := controller.GetRuntimeStatus()
		if status != nil && status.ShellProcStatus == Status_Running {
			go func(id string, c Controller) {
				c.Stop(true, Status_Done, false)
				wstore.DeleteRTInfo(waveobj.MakeORef(waveobj.OType_Block, id))
			}(blockId, controller)
		}
	}
}

func getBoolFromMeta(meta map[string]any, key string, def bool) bool {
	ival, found := meta[key]
	if !found || ival == nil {
		return def
	}
	if val, ok := ival.(bool); ok {
		return val
	}
	return def
}

func getTermSize(bdata *waveobj.Block) waveobj.TermSize {
	if bdata.RuntimeOpts != nil {
		return bdata.RuntimeOpts.TermSize
	} else {
		return waveobj.TermSize{
			Rows: 25,
			Cols: 80,
		}
	}
}

func HandleAppendBlockFile(blockId string, blockFile string, data []byte) error {
	ctx, cancelFn := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancelFn()
	err := filestore.WFS.AppendData(ctx, blockId, blockFile, data)
	if err != nil {
		return fmt.Errorf("error appending to blockfile: %w", err)
	}
	wps.Broker.Publish(wps.WaveEvent{
		Event: wps.Event_BlockFile,
		Scopes: []string{
			waveobj.MakeORef(waveobj.OType_Block, blockId).String(),
		},
		Data: &wps.WSFileEventData{
			ZoneId:   blockId,
			FileName: blockFile,
			FileOp:   wps.FileOp_Append,
			Data64:   base64.StdEncoding.EncodeToString(data),
		},
	})
	return nil
}

func HandleTruncateBlockFile(blockId string) error {
	ctx, cancelFn := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancelFn()
	err := filestore.WFS.WriteFile(ctx, blockId, wavebase.BlockFile_Term, nil)
	if err == fs.ErrNotExist {
		return nil
	}
	if err != nil {
		return fmt.Errorf("error truncating blockfile: %w", err)
	}
	err = filestore.WFS.DeleteFile(ctx, blockId, wavebase.BlockFile_Cache)
	if err == fs.ErrNotExist {
		err = nil
	}
	if err != nil {
		log.Printf("error deleting cache file (continuing): %v\n", err)
	}
	wps.Broker.Publish(wps.WaveEvent{
		Event:  wps.Event_BlockFile,
		Scopes: []string{waveobj.MakeORef(waveobj.OType_Block, blockId).String()},
		Data: &wps.WSFileEventData{
			ZoneId:   blockId,
			FileName: wavebase.BlockFile_Term,
			FileOp:   wps.FileOp_Truncate,
		},
	})
	return nil

}

func debugLog(ctx context.Context, fmtStr string, args ...interface{}) {
	blocklogger.Infof(ctx, "[conndebug] "+fmtStr, args...)
	log.Printf(fmtStr, args...)
}

func CheckConnStatus(blockId string) error {
	bdata, err := wstore.DBMustGet[*waveobj.Block](context.Background(), blockId)
	if err != nil {
		return fmt.Errorf("error getting block: %w", err)
	}
	connName := bdata.Meta.GetString(waveobj.MetaKey_Connection, "")
	if conncontroller.IsLocalConnName(connName) {
		return nil
	}
	if strings.HasPrefix(connName, "wsl://") {
		distroName := strings.TrimPrefix(connName, "wsl://")
		conn := wslconn.GetWslConn(distroName)
		connStatus := conn.DeriveConnStatus()
		if connStatus.Status != conncontroller.Status_Connected {
			return fmt.Errorf("not connected: %s", connStatus.Status)
		}
		return nil
	}
	opts, err := remote.ParseOpts(connName)
	if err != nil {
		return fmt.Errorf("error parsing connection name: %w", err)
	}
	conn := conncontroller.MaybeGetConn(opts)
	if conn == nil {
		return fmt.Errorf("no connection found")
	}
	connStatus := conn.DeriveConnStatus()
	if connStatus.Status != conncontroller.Status_Connected {
		return fmt.Errorf("not connected: %s", connStatus.Status)
	}
	return nil
}

func makeSwapToken(ctx context.Context, logCtx context.Context, blockId string, blockMeta waveobj.MetaMapType, remoteName string, shellType string) *shellutil.TokenSwapEntry {
	token := &shellutil.TokenSwapEntry{
		Token: uuid.New().String(),
		Env:   make(map[string]string),
		Exp:   time.Now().Add(5 * time.Minute),
	}
	token.Env["TERM_PROGRAM"] = "waveterm"
	token.Env["WAVETERM_BLOCKID"] = blockId
	token.Env["WAVETERM_VERSION"] = wavebase.WaveVersion
	token.Env["WAVETERM"] = "1"
	tabId, err := wstore.DBFindTabForBlockId(ctx, blockId)
	if err != nil {
		log.Printf("error finding tab for block: %v\n", err)
	} else {
		token.Env["WAVETERM_TABID"] = tabId
	}
	if tabId != "" {
		wsId, err := wstore.DBFindWorkspaceForTabId(ctx, tabId)
		if err != nil {
			log.Printf("error finding workspace for tab: %v\n", err)
		} else {
			token.Env["WAVETERM_WORKSPACEID"] = wsId
		}
	}
	token.Env["WAVETERM_CLIENTID"] = wstore.GetClientId()
	token.Env["WAVETERM_CONN"] = remoteName
	envMap, err := resolveEnvMap(blockId, blockMeta, remoteName)
	if err != nil {
		log.Printf("error resolving env map: %v\n", err)
	}
	for k, v := range envMap {
		token.Env[k] = v
	}
	token.ScriptText = getCustomInitScript(logCtx, blockMeta, remoteName, shellType)
	return token
}
