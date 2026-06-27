package blockcontroller

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wavetermdev/waveterm/pkg/filestore"
	"github.com/wavetermdev/waveterm/pkg/remote"
	"github.com/wavetermdev/waveterm/pkg/remote/conncontroller"
	"github.com/wavetermdev/waveterm/pkg/sessiondaemon"
	"github.com/wavetermdev/waveterm/pkg/shellexec"
	"github.com/wavetermdev/waveterm/pkg/util/shellutil"
	"github.com/wavetermdev/waveterm/pkg/utilds"
	"github.com/wavetermdev/waveterm/pkg/wavebase"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wcore"
	"github.com/wavetermdev/waveterm/pkg/wps"
	"github.com/wavetermdev/waveterm/pkg/wshrpc"
	"github.com/wavetermdev/waveterm/pkg/wshrpc/wshclient"
	"github.com/wavetermdev/waveterm/pkg/wshutil"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

type SessionDaemonController struct {
	Lock *sync.Mutex

	BlockId        string
	ConnName       string
	DaemonId       string
	TabId          string
	InputSessionId string
	inputSeqNum    int
	versionTs      utilds.VersionTs
}

func MakeSessionDaemonController(tabId string, blockId string, connName string) *SessionDaemonController {
	return &SessionDaemonController{
		Lock:           &sync.Mutex{},
		BlockId:        blockId,
		ConnName:       connName,
		TabId:          tabId,
		InputSessionId: uuid.New().String(),
	}
}

func (sdc *SessionDaemonController) WithLock(f func()) {
	sdc.Lock.Lock()
	defer sdc.Lock.Unlock()
	f()
}

func (sdc *SessionDaemonController) getNextInputSeq() (string, int) {
	sdc.Lock.Lock()
	defer sdc.Lock.Unlock()
	sdc.inputSeqNum++
	return sdc.InputSessionId, sdc.inputSeqNum
}

func (sdc *SessionDaemonController) Start(ctx context.Context, blockMeta waveobj.MetaMapType, rtOpts *waveobj.RuntimeOpts, force bool) error {
	daemon := sessiondaemon.Manager.Get(sdc.DaemonId)
	if daemon == nil {
		log.Printf("[sessiondaemon] start: daemon %s not found in manager", sdc.DaemonId)
		return fmt.Errorf("session daemon %s not found in manager", sdc.DaemonId)
	}

	sessiondaemon.Manager.AttachBlock(ctx, sdc.DaemonId, sdc.BlockId)

	ensureResult, err := sessiondaemon.Manager.EnsureJobState(ctx, sdc.DaemonId, rtOpts, true)
	if err != nil {
		return err
	}
	switch ensureResult.Action {
	case sessiondaemon.DaemonEnsure_Ready:
		sdc.incrementVersion()
		sdc.sendControllerStatus()
		return nil
	case sessiondaemon.DaemonEnsure_Wait:
		return ErrSessionDaemonJobUnknown
	case sessiondaemon.DaemonEnsure_Fallback:
		log.Printf("[sessiondaemon] start: daemon=%s is done, falling back block=%s to shell", sdc.DaemonId, sdc.BlockId)
		return fallbackSessionDaemonToShell(ctx, sdc.DaemonId, sdc.BlockId)
	case sessiondaemon.DaemonEnsure_Start:
		return sdc.createJobAndSync(ctx, blockMeta, rtOpts)
	}

	return fmt.Errorf("unknown session daemon ensure action %q", ensureResult.Action)
}

// createJobAndSync starts a new remote job for the daemon and syncs
// the resulting JobId to all attached blocks so the frontend can
// switch its zoneId.
func (sdc *SessionDaemonController) createJobAndSync(ctx context.Context, blockMeta waveobj.MetaMapType, rtOpts *waveobj.RuntimeOpts) error {
	fsErr := filestore.WFS.MakeFile(ctx, sdc.BlockId, wavebase.BlockFile_Term, nil, wshrpc.FileOpts{MaxSize: DefaultTermMaxFileSize, Circular: true})
	if fsErr != nil && fsErr != fs.ErrExist {
		return fmt.Errorf("error creating block term file: %w", fsErr)
	}
	jobId, err := sdc.startNewJob(ctx, blockMeta, rtOpts)
	if err != nil {
		log.Printf("[sessiondaemon] start: new job failed block=%s err=%v", sdc.BlockId, err)
		return fmt.Errorf("failed to start job: %w", err)
	}

	err = sessiondaemon.Manager.SetJobRunning(ctx, sdc.DaemonId, jobId)
	if err != nil {
		log.Printf("[sessiondaemon] start: set job id failed daemon=%s job=%s err=%v", sdc.DaemonId, jobId, err)
		return fmt.Errorf("failed to set job id on daemon: %w", err)
	}

	sdc.syncJobIdToBlocks(ctx, jobId)

	sdc.incrementVersion()
	sdc.sendControllerStatus()
	return nil
}

// syncJobIdToBlocks writes the daemon's JobId to every attached block's
// DB record so the frontend useEffect picks up the change and calls
// attachToDaemon, switching the terminal zoneId to the new job's output stream.
func (sdc *SessionDaemonController) syncJobIdToBlocks(ctx context.Context, jobId string) {
	attachedBlocks := sessiondaemon.Manager.GetBlocksForDaemon(sdc.DaemonId)
	for _, blockId := range attachedBlocks {
		wstore.DBUpdateFn(ctx, blockId, func(block *waveobj.Block) {
			block.JobId = jobId
		})
		wcore.SendWaveObjUpdate(waveobj.MakeORef(waveobj.OType_Block, blockId))
	}
}

func (sdc *SessionDaemonController) startNewJob(ctx context.Context, blockMeta waveobj.MetaMapType, rtOpts *waveobj.RuntimeOpts) (string, error) {
	termSize := getLatestTermSize(sdc.BlockId)
	cmdStr := blockMeta.GetString(waveobj.MetaKey_Cmd, "")
	cwd := blockMeta.GetString(waveobj.MetaKey_CmdCwd, "")
	opts, err := remote.ParseOpts(sdc.ConnName)
	if err != nil {
		return "", fmt.Errorf("invalid ssh remote name (%s): %w", sdc.ConnName, err)
	}
	conn := conncontroller.MaybeGetConn(opts)
	if conn == nil {
		return "", fmt.Errorf("connection %q not found", sdc.ConnName)
	}
	connRoute := wshutil.MakeConnectionRouteId(sdc.ConnName)
	remoteInfo, err := wshclient.RemoteGetInfoCommand(wshclient.GetBareRpcClient(), &wshrpc.RpcOpts{Route: connRoute, Timeout: 2000})
	if err != nil {
		return "", fmt.Errorf("unable to obtain remote info from connserver: %w", err)
	}
	shellType := shellutil.GetShellTypeFromShellPath(remoteInfo.Shell)
	swapToken := makeSwapToken(ctx, ctx, sdc.BlockId, blockMeta, sdc.ConnName, shellType)
	sockName := wavebase.GetPersistentRemoteSockName(wstore.GetClientId())
	err = attachRpcContextToSwapToken(swapToken, sdc.BlockId, sdc.ConnName, sockName)
	if err != nil {
		return "", err
	}
	cmdOpts := shellexec.CommandOptsType{
		Interactive: true,
		Login:       true,
		Cwd:         cwd,
		SwapToken:   swapToken,
		ForceJwt:    blockMeta.GetBool(waveobj.MetaKey_CmdJwt, false),
	}
	jobId, err := shellexec.StartRemoteShellJob(ctx, ctx, termSize, cmdStr, cmdOpts, conn, sdc.BlockId)
	if err != nil {
		return "", fmt.Errorf("failed to start remote shell job: %w", err)
	}

	wstore.DBUpdateFn(ctx, jobId, func(job *waveobj.Job) {
		job.AttachedBlockId = "daemon:" + sdc.DaemonId
	})

	return jobId, nil
}

func (sdc *SessionDaemonController) Stop(graceful bool, newStatus string, destroy bool) {
	if !destroy {
		return
	}
	ctx := context.Background()
	sessiondaemon.Manager.DetachBlock(ctx, sdc.DaemonId, sdc.BlockId)
}

func (sdc *SessionDaemonController) SendInput(inputUnion *BlockInputUnion) error {
	if inputUnion == nil {
		return nil
	}
	daemon := sessiondaemon.Manager.Get(sdc.DaemonId)
	if daemon == nil {
		return fmt.Errorf("session daemon %s not found", sdc.DaemonId)
	}
	return daemon.SendInput(context.Background(), inputUnion.InputData, inputUnion.SigName, inputUnion.TermSize)
}

func (sdc *SessionDaemonController) ApplyTermSize(termSize waveobj.TermSize) error {
	daemon := sessiondaemon.Manager.Get(sdc.DaemonId)
	if daemon == nil {
		return nil
	}
	return daemon.SendInput(context.Background(), nil, "", &termSize)
}

func (sdc *SessionDaemonController) GetRuntimeStatus() *BlockControllerRuntimeStatus {
	var rtn BlockControllerRuntimeStatus
	sdc.WithLock(func() {
		rtn.BlockId = sdc.BlockId
		rtn.ShellProcConnName = sdc.ConnName
		rtn.Version = sdc.versionTs.GetVersionTs()
		daemon := sessiondaemon.Manager.Get(sdc.DaemonId)
		if daemon != nil {
			if daemon.JobId == "" {
				rtn.ShellProcStatus = "init"
			} else {
				rtn.ShellProcStatus = "running"
			}
		} else {
			rtn.ShellProcStatus = "done"
		}
	})
	return &rtn
}

func (sdc *SessionDaemonController) incrementVersion() {
	sdc.versionTs.GetVersionTs()
}

func (sdc *SessionDaemonController) GetConnName() string {
	return sdc.ConnName
}

func (sdc *SessionDaemonController) sendControllerStatus() {
	rtStatus := sdc.GetRuntimeStatus()
	log.Printf("sending blockcontroller update %#v\n", rtStatus)
	wps.Broker.Publish(wps.WaveEvent{
		Event: wps.Event_ControllerStatus,
		Scopes: []string{
			waveobj.MakeORef(waveobj.OType_Tab, sdc.TabId).String(),
			waveobj.MakeORef(waveobj.OType_Block, sdc.BlockId).String(),
		},
		Data: rtStatus,
	})
}

func autoCreateSessionDaemon(ctx context.Context, blockId string, blockMeta waveobj.MetaMapType, connName string, rtOpts *waveobj.RuntimeOpts) (string, error) {
	dbDaemon := &waveobj.SessionDaemon{
		OID:         uuid.New().String(),
		Name:        "",
		Connection:  connName,
		IsAnonymous: true,
		Status:      sessiondaemon.Status_Init,
		CreatedAt:   time.Now().UnixMilli(),
		IdleTimeout: sessiondaemon.DefaultAnonymousIdleTimeout,
	}

	err := wstore.DBInsert(ctx, dbDaemon)
	if err != nil {
		return "", fmt.Errorf("insert session daemon: %w", err)
	}

	err = wstore.DBUpdateFn(ctx, blockId, func(block *waveobj.Block) {
		block.Meta[waveobj.MetaKey_SessionDaemonId] = dbDaemon.OID
		delete(block.Meta, MetaKey_SessionNoAutoCreate)
	})
	if err != nil {
		return "", fmt.Errorf("update block meta: %w", err)
	}

	_, err = sessiondaemon.Manager.GetOrCreate(ctx, dbDaemon)
	if err != nil {
		return "", fmt.Errorf("create session daemon in manager: %w", err)
	}

	return dbDaemon.OID, nil
}
