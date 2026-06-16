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
	log.Printf("[sessiondaemon] start: block=%s daemon=%s conn=%s force=%v", sdc.BlockId, sdc.DaemonId, sdc.ConnName, force)

	daemon := sessiondaemon.Manager.Get(sdc.DaemonId)
	if daemon == nil {
		log.Printf("[sessiondaemon] start: daemon %s not found in manager", sdc.DaemonId)
		return fmt.Errorf("session daemon %s not found in manager", sdc.DaemonId)
	}

	sessiondaemon.Manager.AttachBlock(ctx, sdc.DaemonId, sdc.BlockId)

	dbDaemon, err := wstore.DBMustGet[*waveobj.SessionDaemon](ctx, sdc.DaemonId)
	if err != nil {
		return fmt.Errorf("error getting session daemon: %w", err)
	}

	if dbDaemon.Status == sessiondaemon.Status_Done {
		return fmt.Errorf("remote job manager has exited, restart or delete the session")
	}
	if dbDaemon.Status == sessiondaemon.Status_Disconnected {
		return fmt.Errorf("daemon is disconnected, waiting for connection to recover")
	}

	if dbDaemon.JobId != "" {
		return sdc.tryReconnect(ctx, daemon, dbDaemon, rtOpts)
	}

	return sdc.createJobAndSync(ctx, blockMeta, rtOpts)
}

// tryReconnect attempts to reconnect to the daemon's existing job.
func (sdc *SessionDaemonController) tryReconnect(ctx context.Context, daemon *sessiondaemon.SessionDaemon, dbDaemon *waveobj.SessionDaemon, rtOpts *waveobj.RuntimeOpts) error {
	log.Printf("[sessiondaemon] start: attempting reconnect to job=%s status=%s", dbDaemon.JobId, dbDaemon.Status)
	err := daemon.Reconnect(ctx, dbDaemon, rtOpts)
	if err == nil {
		log.Printf("[sessiondaemon] start: reconnect ok block=%s job=%s", sdc.BlockId, dbDaemon.JobId)
		sdc.incrementVersion()
		sdc.sendControllerStatus()
		return nil
	}
	log.Printf("[sessiondaemon] start: reconnect failed block=%s job=%s err=%v", sdc.BlockId, dbDaemon.JobId, err)

	dbDaemon, dbErr := wstore.DBMustGet[*waveobj.SessionDaemon](ctx, sdc.DaemonId)
	if dbErr != nil {
		return fmt.Errorf("error reading daemon after reconnect failure: %w", dbErr)
	}
	switch dbDaemon.Status {
	case sessiondaemon.Status_Disconnected:
		return fmt.Errorf("daemon is disconnected, waiting for connection to recover")
	case sessiondaemon.Status_Done:
		return fmt.Errorf("remote job manager has exited, restart or delete the session")
	default:
		return fmt.Errorf("unexpected daemon status %q after reconnect failure", dbDaemon.Status)
	}
}

// createJobAndSync starts a new remote job for the daemon and syncs
// the resulting JobId to all attached blocks so the frontend can
// switch its zoneId.
func (sdc *SessionDaemonController) createJobAndSync(ctx context.Context, blockMeta waveobj.MetaMapType, rtOpts *waveobj.RuntimeOpts) error {
	log.Printf("[sessiondaemon] start: starting new job block=%s", sdc.BlockId)
	fsErr := filestore.WFS.MakeFile(ctx, sdc.BlockId, wavebase.BlockFile_Term, nil, wshrpc.FileOpts{MaxSize: DefaultTermMaxFileSize, Circular: true})
	if fsErr != nil && fsErr != fs.ErrExist {
		return fmt.Errorf("error creating block term file: %w", fsErr)
	}
	jobId, err := sdc.startNewJob(ctx, blockMeta, rtOpts)
	if err != nil {
		log.Printf("[sessiondaemon] start: new job failed block=%s err=%v", sdc.BlockId, err)
		return fmt.Errorf("failed to start job: %w", err)
	}
	log.Printf("[sessiondaemon] start: new job started block=%s job=%s", sdc.BlockId, jobId)

	daemon := sessiondaemon.Manager.Get(sdc.DaemonId)
	if daemon == nil {
		return fmt.Errorf("session daemon %s not found in manager", sdc.DaemonId)
	}

	err = daemon.SetJobId(ctx, jobId)
	if err != nil {
		log.Printf("[sessiondaemon] start: set job id failed daemon=%s job=%s err=%v", sdc.DaemonId, jobId, err)
		return fmt.Errorf("failed to set job id on daemon: %w", err)
	}

	sdc.syncJobIdToBlocks(ctx, jobId)

	log.Printf("[sessiondaemon] start: done block=%s daemon=%s job=%s", sdc.BlockId, sdc.DaemonId, jobId)
	sdc.incrementVersion()
	sdc.sendControllerStatus()
	return nil
}

// syncJobIdToBlocks writes the daemon's JobId to every attached block's
// DB record so the frontend useEffect picks up the change and calls
// attachToDaemon, switching the terminal zoneId to the new job's output stream.
func (sdc *SessionDaemonController) syncJobIdToBlocks(ctx context.Context, jobId string) {
	attachedBlocks := sessiondaemon.Manager.GetBlocksForDaemon(sdc.DaemonId)
	log.Printf("[sessiondaemon] start: syncing jobId=%s to %d attached blocks for daemon=%s", jobId, len(attachedBlocks), sdc.DaemonId)
	for _, blockId := range attachedBlocks {
		wstore.DBUpdateFn(ctx, blockId, func(block *waveobj.Block) {
			block.JobId = jobId
		})
		wcore.SendWaveObjUpdate(waveobj.MakeORef(waveobj.OType_Block, blockId))
		log.Printf("[sessiondaemon] start: synced jobId=%s to block=%s", jobId, blockId)
	}
}

func (sdc *SessionDaemonController) startNewJob(ctx context.Context, blockMeta waveobj.MetaMapType, rtOpts *waveobj.RuntimeOpts) (string, error) {
	log.Printf("[sessiondaemon] startNewJob: block=%s conn=%s", sdc.BlockId, sdc.ConnName)
	termSize := waveobj.TermSize{
		Rows: shellutil.DefaultTermRows,
		Cols: shellutil.DefaultTermCols,
	}
	if rtOpts != nil && rtOpts.TermSize.Rows > 0 && rtOpts.TermSize.Cols > 0 {
		termSize = rtOpts.TermSize
	}
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
	rpcContext := wshrpc.RpcContext{
		ProcRoute: true,
		SockName:  sockName,
		BlockId:   sdc.BlockId,
		Conn:      sdc.ConnName,
	}
	jwtStr, err := wshutil.MakeClientJWTToken(rpcContext)
	if err != nil {
		return "", fmt.Errorf("error making jwt token: %w", err)
	}
	swapToken.RpcContext = &rpcContext
	swapToken.Env[wshutil.WaveJwtTokenVarName] = jwtStr
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
	log.Printf("[sessiondaemon] stop: block=%s daemon=%s remaining=%d",
		sdc.BlockId, sdc.DaemonId, len(sessiondaemon.Manager.GetBlocksForDaemon(sdc.DaemonId)))
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
	log.Printf("[sessiondaemon] GetRuntimeStatus: block=%s daemon=%s status=%s version=%d", rtn.BlockId, sdc.DaemonId, rtn.ShellProcStatus, rtn.Version)
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
	log.Printf("[sessiondaemon] autoCreate: block=%s conn=%s", blockId, connName)
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
	})
	if err != nil {
		return "", fmt.Errorf("update block meta: %w", err)
	}

	_, err = sessiondaemon.Manager.GetOrCreate(ctx, dbDaemon)
	if err != nil {
		return "", fmt.Errorf("create session daemon in manager: %w", err)
	}

	log.Printf("[sessiondaemon] autoCreate: done block=%s daemon=%s", blockId, dbDaemon.OID)
	return dbDaemon.OID, nil
}
