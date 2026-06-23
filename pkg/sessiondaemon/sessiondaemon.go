package sessiondaemon

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wavetermdev/waveterm/pkg/jobcontroller"
	"github.com/wavetermdev/waveterm/pkg/remote/conncontroller"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wshrpc"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

const (
	DefaultAnonymousIdleTimeout = 600   // 10min
	DefaultNamedIdleTimeout     = 86400 // 24h
	IdleCheckInterval           = 60    // 60s
	DoneReapTimeout             = 300   // 5min for done daemons with no blocks
)

const (
	Status_Init         = "init"
	Status_Running      = "running"
	Status_Disconnected = "disconnected"
	Status_Done         = "done"
)

const (
	JobManagerState_Alive   = "alive"
	JobManagerState_Dead    = "dead"
	JobManagerState_Unknown = "unknown"
)

const (
	DaemonEnsure_Ready    = "ready"
	DaemonEnsure_Wait     = "wait"
	DaemonEnsure_Fallback = "fallback"
	DaemonEnsure_Start    = "start"
)

type SessionDaemon struct {
	Lock sync.Mutex

	DaemonId       string
	Name           string
	JobId          string
	InputSessionId string
	SeqNum         int
	Blocks         map[string]bool
}

type SessionDaemonManager struct {
	Lock    sync.Mutex
	Daemons map[string]*SessionDaemon
}

type EnsureResult struct {
	Action string
	JobId  string
}

var Manager = &SessionDaemonManager{
	Daemons: make(map[string]*SessionDaemon),
}

var OnDaemonJobDoneFn func(ctx context.Context, daemonId string)

func init() {
	jobcontroller.ClearSessionDaemonJobFn = func(ctx context.Context, jobId string) {
		Manager.ClearJobIdFromDaemons(ctx, jobId)
	}
	jobcontroller.OnConnectionUpFn = func(ctx context.Context, connName string) {
		Manager.OnConnectionUp(ctx, connName)
	}
	jobcontroller.GetSessionDaemonBlocksFn = func(daemonId string) []string {
		return Manager.GetBlocksForDaemon(daemonId)
	}
}

func (sd *SessionDaemon) GetNextInputSeq() (string, int) {
	sd.Lock.Lock()
	defer sd.Lock.Unlock()
	sd.SeqNum++
	return sd.InputSessionId, sd.SeqNum
}

func (sd *SessionDaemon) HasAttachedBlocks() bool {
	sd.Lock.Lock()
	defer sd.Lock.Unlock()
	return len(sd.Blocks) > 0
}

func (sd *SessionDaemon) HasBlock(blockId string) bool {
	sd.Lock.Lock()
	defer sd.Lock.Unlock()
	return sd.Blocks[blockId]
}

func (sd *SessionDaemon) Reconnect(ctx context.Context, dbDaemon *waveobj.SessionDaemon, rtOpts *waveobj.RuntimeOpts) error {
	if dbDaemon.JobId == "" {
		return fmt.Errorf("no jobid to reconnect")
	}
	sd.Lock.Lock()
	sd.JobId = dbDaemon.JobId
	sd.Lock.Unlock()

	err := jobcontroller.ReconnectJob(ctx, dbDaemon.JobId, rtOpts)
	if err != nil {
		var jobGone bool
		dbErr := wstore.DBUpdateFn(ctx, dbDaemon.OID, func(dbSd *waveobj.SessionDaemon) {
			if dbSd.JobId == "" {
				dbSd.Status = Status_Done
				jobGone = true
			} else {
				dbSd.Status = Status_Disconnected
			}
		})
		if dbErr != nil {
			log.Printf("[sessiondaemon:%s] reconnect: error updating status: %v (memory may be stale)", sd.DaemonId, dbErr)
			// If the DB write failed, jobGone is unreliable — do NOT clear memory JobId.
			return fmt.Errorf("reconnect failed: %w", err)
		}
		if jobGone {
			sd.Lock.Lock()
			sd.JobId = ""
			sd.Lock.Unlock()
			log.Printf("[sessiondaemon:%s] reconnect: job manager gone, status -> done", sd.DaemonId)
			return fmt.Errorf("job manager has exited")
		}
		log.Printf("[sessiondaemon:%s] reconnect: failed, status -> disconnected: %v", sd.DaemonId, err)
		return fmt.Errorf("reconnect failed: %w", err)
	}

	if err := wstore.DBUpdateFn(ctx, dbDaemon.OID, func(dbSd *waveobj.SessionDaemon) {
		dbSd.Status = Status_Running
	}); err != nil {
		log.Printf("[sessiondaemon:%s] reconnect: error updating status to running: %v", sd.DaemonId, err)
	}
	log.Printf("[sessiondaemon:%s] reconnect: success, status -> running", sd.DaemonId)
	return nil
}

func (sd *SessionDaemon) Stop(ctx context.Context) error {
	sd.Lock.Lock()
	jobId := sd.JobId
	sd.Lock.Unlock()
	log.Printf("[sessiondaemon] stop daemon=%s job=%s", sd.DaemonId, jobId)
	if jobId != "" {
		err := jobcontroller.TerminateAndDetachJob(ctx, jobId)
		if err != nil {
			log.Printf("[sessiondaemon:%s] error terminating remote job %s: %v", sd.DaemonId, jobId, err)
			return fmt.Errorf("failed to terminate remote job: %w", err)
		}
	}
	return nil
}

func (sd *SessionDaemon) SendInput(ctx context.Context, inputData []byte, sigName string, termSize *waveobj.TermSize) error {
	sd.Lock.Lock()
	jobId := sd.JobId
	if jobId == "" {
		sd.Lock.Unlock()
		return fmt.Errorf("no job attached")
	}
	sd.SeqNum++
	inputSessionId, seqNum := sd.InputSessionId, sd.SeqNum
	sd.Lock.Unlock()

	data := wshrpc.CommandJobInputData{
		JobId:          jobId,
		InputSessionId: inputSessionId,
		SeqNum:         seqNum,
		TermSize:       termSize,
		SigName:        sigName,
	}
	if len(inputData) > 0 {
		data.InputData64 = base64.StdEncoding.EncodeToString(inputData)
	}
	return jobcontroller.SendInput(ctx, data)
}

func (sd *SessionDaemonManager) GetOrCreate(ctx context.Context, dbDaemon *waveobj.SessionDaemon) (*SessionDaemon, error) {
	sd.Lock.Lock()
	defer sd.Lock.Unlock()

	if existing, ok := sd.Daemons[dbDaemon.OID]; ok {
		existing.Lock.Lock()
		if existing.JobId == "" {
			existing.JobId = dbDaemon.JobId
		}
		existing.Lock.Unlock()
		return existing, nil
	}

	daemon := &SessionDaemon{
		DaemonId:       dbDaemon.OID,
		Name:           dbDaemon.Name,
		JobId:          dbDaemon.JobId,
		InputSessionId: uuid.New().String(),
		Blocks:         make(map[string]bool),
	}
	sd.Daemons[dbDaemon.OID] = daemon
	return daemon, nil
}

func (sd *SessionDaemonManager) Get(daemonId string) *SessionDaemon {
	sd.Lock.Lock()
	defer sd.Lock.Unlock()
	return sd.Daemons[daemonId]
}

func (sd *SessionDaemonManager) Remove(daemonId string) {
	sd.Lock.Lock()
	defer sd.Lock.Unlock()
	delete(sd.Daemons, daemonId)
}

func (sd *SessionDaemonManager) SetJobRunning(ctx context.Context, daemonId string, jobId string) error {
	daemon := sd.Get(daemonId)
	var oldJobId string
	if daemon != nil {
		daemon.Lock.Lock()
		oldJobId = daemon.JobId
		daemon.JobId = jobId
		daemon.Lock.Unlock()
	}

	err := wstore.DBUpdateFn(ctx, daemonId, func(sdDb *waveobj.SessionDaemon) {
		sdDb.JobId = jobId
		sdDb.Status = Status_Running
	})
	if err != nil {
		if daemon != nil {
			daemon.Lock.Lock()
			daemon.JobId = oldJobId
			daemon.Lock.Unlock()
		}
		log.Printf("[sessiondaemon:%s] SetJobRunning: DB update failed: %v", daemonId, err)
		return err
	}
	return nil
}

func (sd *SessionDaemonManager) clearJobDone(ctx context.Context, daemonId string) error {
	daemon := sd.Get(daemonId)
	var oldJobId string
	if daemon != nil {
		daemon.Lock.Lock()
		oldJobId = daemon.JobId
		daemon.JobId = ""
		daemon.Lock.Unlock()
	}

	if err := wstore.DBUpdateFn(ctx, daemonId, func(dbSd *waveobj.SessionDaemon) {
		dbSd.JobId = ""
		dbSd.Status = Status_Done
	}); err != nil {
		if daemon != nil {
			daemon.Lock.Lock()
			daemon.JobId = oldJobId
			daemon.Lock.Unlock()
		}
		return err
	}
	return nil
}

func (sd *SessionDaemonManager) AttachBlock(ctx context.Context, daemonId string, blockId string) {
	sd.Lock.Lock()
	daemon, ok := sd.Daemons[daemonId]
	if !ok {
		sd.Lock.Unlock()
		return
	}
	daemon.Lock.Lock()
	sd.Lock.Unlock()
	defer daemon.Lock.Unlock()
	daemon.Blocks[blockId] = true
	sd.resetIdleTimer(ctx, daemonId)
}

func (sd *SessionDaemonManager) DetachBlock(ctx context.Context, daemonId string, blockId string) {
	sd.Lock.Lock()
	daemon, ok := sd.Daemons[daemonId]
	if !ok {
		sd.Lock.Unlock()
		return
	}
	daemon.Lock.Lock()
	sd.Lock.Unlock()
	defer daemon.Lock.Unlock()
	delete(daemon.Blocks, blockId)
	if len(daemon.Blocks) == 0 {
		sd.startIdleCountdown(ctx, daemonId)
	}
}

// --- idle timer helpers ---
// These centralize IdleSince management so there is a single place
// to understand the countdown mechanics.

func (sd *SessionDaemonManager) resetIdleTimer(ctx context.Context, daemonId string) {
	err := wstore.DBUpdateFn(ctx, daemonId, func(dbD *waveobj.SessionDaemon) {
		dbD.IdleSince = 0
	})
	if err != nil {
		log.Printf("[sessiondaemon:%s] error resetting idle timer: %v", daemonId, err)
	}
}

func (sd *SessionDaemonManager) startIdleCountdown(ctx context.Context, daemonId string) {
	err := wstore.DBUpdateFn(ctx, daemonId, func(dbD *waveobj.SessionDaemon) {
		if dbD.Status == Status_Done {
			dbD.IdleSince = DoneReapTimeout
			return
		}
		dbD.IdleSince = dbD.IdleTimeout
	})
	if err != nil {
		log.Printf("[sessiondaemon:%s] error starting idle countdown: %v", daemonId, err)
	}
}

// advanceIdleTimer decrements IdleSince and returns the new value.
// A return value <= 0 means the timer has expired.  Returns 0 on error.
func (sd *SessionDaemonManager) advanceIdleTimer(ctx context.Context, daemonId string) int64 {
	var remaining int64
	err := wstore.DBUpdateFn(ctx, daemonId, func(dbD *waveobj.SessionDaemon) {
		dbD.IdleSince -= IdleCheckInterval
		remaining = dbD.IdleSince
	})
	if err != nil {
		log.Printf("[sessiondaemon:%s] error advancing idle timer: %v", daemonId, err)
		return 0
	}
	return remaining
}

func (sd *SessionDaemonManager) GetBlocksForDaemon(daemonId string) []string {
	sd.Lock.Lock()
	daemon, ok := sd.Daemons[daemonId]
	if !ok {
		sd.Lock.Unlock()
		return nil
	}
	daemon.Lock.Lock()
	sd.Lock.Unlock()
	defer daemon.Lock.Unlock()
	var rtn []string
	for blockId := range daemon.Blocks {
		rtn = append(rtn, blockId)
	}
	return rtn
}

func (sd *SessionDaemonManager) SendInput(daemonId string, inputData []byte, sigName string, termSize *waveobj.TermSize) error {
	ctx := context.Background()
	sd.Lock.Lock()
	daemon, ok := sd.Daemons[daemonId]
	sd.Lock.Unlock()
	if !ok {
		return fmt.Errorf("daemon %s not found", daemonId)
	}
	return daemon.SendInput(ctx, inputData, sigName, termSize)
}

func (sd *SessionDaemonManager) MarkDone(ctx context.Context, daemonId string) error {
	if err := sd.clearJobDone(ctx, daemonId); err != nil {
		log.Printf("[sessiondaemon:%s] MarkDone: DB update failed: %v", daemonId, err)
		return err
	}
	log.Printf("[sessiondaemon:%s] MarkDone: job cleared, status=done", daemonId)
	return nil
}

func ClassifyJobManagerState(ctx context.Context, dbDaemon *waveobj.SessionDaemon) (string, error) {
	if dbDaemon == nil || dbDaemon.JobId == "" {
		return JobManagerState_Dead, nil
	}
	job, err := wstore.DBGet[*waveobj.Job](ctx, dbDaemon.JobId)
	if err != nil {
		return JobManagerState_Unknown, fmt.Errorf("get job %s: %w", dbDaemon.JobId, err)
	}
	if job == nil || job.JobManagerStatus == jobcontroller.JobManagerStatus_Done {
		return JobManagerState_Dead, nil
	}
	if job.JobManagerPid == 0 {
		return JobManagerState_Unknown, nil
	}
	connected, err := conncontroller.IsConnected(dbDaemon.Connection)
	if err != nil {
		return JobManagerState_Unknown, err
	}
	if !connected {
		return JobManagerState_Unknown, nil
	}
	alive, err := conncontroller.CheckRemoteProcessAlive(ctx, dbDaemon.Connection, job.JobManagerPid)
	if err != nil {
		return JobManagerState_Unknown, nil
	}
	if alive {
		return JobManagerState_Alive, nil
	}
	return JobManagerState_Dead, nil
}

func (sd *SessionDaemonManager) EnsureJobState(ctx context.Context, daemonId string, rtOpts *waveobj.RuntimeOpts, reconnect bool) (*EnsureResult, error) {
	dbDaemon, err := wstore.DBMustGet[*waveobj.SessionDaemon](ctx, daemonId)
	if err != nil {
		return nil, fmt.Errorf("get session daemon: %w", err)
	}

	memDaemon, err := sd.GetOrCreate(ctx, dbDaemon)
	if err != nil {
		return nil, fmt.Errorf("create session daemon in manager: %w", err)
	}

	if dbDaemon.Status == Status_Done {
		return &EnsureResult{Action: DaemonEnsure_Fallback}, nil
	}

	if dbDaemon.JobId == "" {
		if dbDaemon.Status == Status_Disconnected {
			return &EnsureResult{Action: DaemonEnsure_Wait}, nil
		}
		return &EnsureResult{Action: DaemonEnsure_Start}, nil
	}

	jobState, err := ClassifyJobManagerState(ctx, dbDaemon)
	if err != nil {
		return nil, fmt.Errorf("check session daemon job manager: %w", err)
	}
	switch jobState {
	case JobManagerState_Dead:
		if err := sd.MarkDone(ctx, daemonId); err != nil {
			return nil, err
		}
		return &EnsureResult{Action: DaemonEnsure_Fallback}, nil
	case JobManagerState_Unknown:
		return &EnsureResult{Action: DaemonEnsure_Wait}, nil
	}

	if !reconnect {
		return &EnsureResult{Action: DaemonEnsure_Ready, JobId: dbDaemon.JobId}, nil
	}

	err = memDaemon.Reconnect(ctx, dbDaemon, rtOpts)
	if err != nil {
		dbDaemon2, dbErr := wstore.DBMustGet[*waveobj.SessionDaemon](ctx, daemonId)
		if dbErr == nil && dbDaemon2.Status == Status_Done {
			return &EnsureResult{Action: DaemonEnsure_Fallback}, nil
		}
		return nil, err
	}
	return &EnsureResult{Action: DaemonEnsure_Ready, JobId: dbDaemon.JobId}, nil
}

// GetMemJobId returns the in-memory JobId for a daemon, used as a
// fallback when the DB read returns stale data (e.g., SessionInfoCommand
// called before a SetJobId transaction is visible).
func (sd *SessionDaemonManager) GetMemJobId(daemonId string) string {
	sd.Lock.Lock()
	daemon, ok := sd.Daemons[daemonId]
	sd.Lock.Unlock()
	if !ok {
		return ""
	}
	daemon.Lock.Lock()
	defer daemon.Lock.Unlock()
	return daemon.JobId
}

// Rename updates the daemon's name and marks it as non-anonymous,
// both in memory and in the database.
func (sd *SessionDaemonManager) Rename(ctx context.Context, daemonId string, name string) error {
	sd.Lock.Lock()
	daemon, ok := sd.Daemons[daemonId]
	sd.Lock.Unlock()
	if ok {
		daemon.Lock.Lock()
		daemon.Name = name
		daemon.Lock.Unlock()
	}
	err := wstore.DBUpdateFn(ctx, daemonId, func(sdDb *waveobj.SessionDaemon) {
		sdDb.Name = name
		sdDb.IsAnonymous = false
	})
	if err != nil {
		return fmt.Errorf("update session daemon: %w", err)
	}
	return nil
}

// RecordActivity updates the daemon's LastActiveAt timestamp in the database.
func (sd *SessionDaemonManager) RecordActivity(ctx context.Context, daemonId string) error {
	err := wstore.DBUpdateFn(ctx, daemonId, func(sdDb *waveobj.SessionDaemon) {
		sdDb.LastActiveAt = time.Now().UnixMilli()
	})
	if err != nil {
		return fmt.Errorf("record session activity: %w", err)
	}
	return nil
}

func (sd *SessionDaemonManager) ClearJobIdFromDaemons(ctx context.Context, jobId string) {
	sd.Lock.Lock()
	var daemonIds []string
	for _, daemon := range sd.Daemons {
		daemon.Lock.Lock()
		if daemon.JobId == jobId {
			daemonIds = append(daemonIds, daemon.DaemonId)
			daemon.Lock.Unlock()
			continue
		}
		daemon.Lock.Unlock()
	}
	sd.Lock.Unlock()

	for _, daemonId := range daemonIds {
		if err := sd.clearJobDone(ctx, daemonId); err != nil {
			log.Printf("[sessiondaemon:%s] ClearJobIdFromDaemons: DB update failed: %v", daemonId, err)
			continue
		}
		log.Printf("[sessiondaemon:%s] ClearJobIdFromDaemons: job=%s cleared, status=done", daemonId, jobId)
		if OnDaemonJobDoneFn != nil {
			OnDaemonJobDoneFn(ctx, daemonId)
		}
	}
}

func (sd *SessionDaemonManager) InitFromDB(ctx context.Context) error {
	daemons, err := wstore.DBGetAllObjsByType[*waveobj.SessionDaemon](ctx, waveobj.OType_SessionDaemon)
	if err != nil {
		return fmt.Errorf("load session daemons: %w", err)
	}

	for _, dbDaemon := range daemons {
		_, err := sd.GetOrCreate(ctx, dbDaemon)
		if err != nil {
			log.Printf("[sessiondaemon] warning: failed to load daemon %s: %v", dbDaemon.OID, err)
			continue
		}

		switch dbDaemon.Status {
		case Status_Running, Status_Disconnected:
			// Do NOT call Reconnect here — connections may not be established yet.
			// Reconnection is deferred to SessionDaemonController.Start() when a
			// block referencing this daemon is resynced and the connection is ready.
			log.Printf("[sessiondaemon:%s] loaded daemon status=%s job=%s (reconnect deferred)", dbDaemon.OID, dbDaemon.Status, dbDaemon.JobId)
		case Status_Done:
			log.Printf("[sessiondaemon:%s] loaded done daemon", dbDaemon.OID)
		case Status_Init:
			log.Printf("[sessiondaemon:%s] loaded init daemon", dbDaemon.OID)
		default:
			log.Printf("[sessiondaemon:%s] unknown status %q, treating as init", dbDaemon.OID, dbDaemon.Status)
			if err := wstore.DBUpdateFn(ctx, dbDaemon.OID, func(dbSd *waveobj.SessionDaemon) {
				dbSd.Status = Status_Init
			}); err != nil {
				log.Printf("[sessiondaemon:%s] error fixing unknown status: %v", dbDaemon.OID, err)
			}
		}
	}

	log.Printf("[sessiondaemon] InitFromDB complete: %d daemons loaded", len(sd.Daemons))
	return nil
}

// OnConnectionUp is called when an SSH connection becomes ready.
// It checks all daemons on that connection: reconnects live jobs and
// cleans up daemons whose remote job manager has died.
func (sd *SessionDaemonManager) OnConnectionUp(ctx context.Context, connName string) {
	daemons, err := wstore.DBGetAllObjsByType[*waveobj.SessionDaemon](ctx, waveobj.OType_SessionDaemon)
	if err != nil {
		return
	}
	for _, dbDaemon := range daemons {
		if dbDaemon.Connection != connName {
			continue
		}
		if dbDaemon.JobId == "" {
			continue
		}

		ensureResult, err := sd.EnsureJobState(ctx, dbDaemon.OID, nil, true)
		if err != nil {
			log.Printf("[sessiondaemon:%s] OnConnectionUp: error checking job manager state: %v", dbDaemon.OID, err)
			continue
		}
		switch ensureResult.Action {
		case DaemonEnsure_Fallback:
			log.Printf("[sessiondaemon:%s] OnConnectionUp: remote job manager dead, falling back", dbDaemon.OID)
			if OnDaemonJobDoneFn != nil {
				OnDaemonJobDoneFn(ctx, dbDaemon.OID)
			}
		case DaemonEnsure_Wait:
			log.Printf("[sessiondaemon:%s] OnConnectionUp: job manager state unknown, waiting", dbDaemon.OID)
		}
	}
}
