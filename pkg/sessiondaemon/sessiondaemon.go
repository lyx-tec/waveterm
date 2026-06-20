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

func (sd *SessionDaemon) SetJobId(ctx context.Context, jobId string) error {
	sd.Lock.Lock()
	oldJobId := sd.JobId
	sd.JobId = jobId
	sd.Lock.Unlock()
	log.Printf("[sessiondaemon:%s] SetJobId: %s -> %s", sd.DaemonId, oldJobId, jobId)

	err := wstore.DBUpdateFn(ctx, sd.DaemonId, func(sdDb *waveobj.SessionDaemon) {
		sdDb.JobId = jobId
		sdDb.Status = Status_Running
	})
	if err != nil {
		// Roll back memory to keep it consistent with DB.
		sd.Lock.Lock()
		sd.JobId = oldJobId
		sd.Lock.Unlock()
		log.Printf("[sessiondaemon:%s] SetJobId: DB update failed, rolled back to %s: %v", sd.DaemonId, oldJobId, err)
		return err
	}
	log.Printf("[sessiondaemon:%s] SetJobId: DB updated (status=running job=%s)", sd.DaemonId, jobId)
	return nil
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
		log.Printf("[sessiondaemon] GetOrCreate: found existing daemon=%s job=%s", dbDaemon.OID, dbDaemon.JobId)
		existing.Lock.Lock()
		if existing.JobId == "" {
			existing.JobId = dbDaemon.JobId
		}
		existing.Lock.Unlock()
		return existing, nil
	}

	log.Printf("[sessiondaemon] GetOrCreate: creating new daemon=%s name=%q", dbDaemon.OID, dbDaemon.Name)
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

func (sd *SessionDaemonManager) AttachBlock(ctx context.Context, daemonId string, blockId string) {
	log.Printf("[sessiondaemon] AttachBlock: daemon=%s block=%s", daemonId, blockId)
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
	log.Printf("[sessiondaemon] DetachBlock: daemon=%s block=%s", daemonId, blockId)
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

// MarkDone clears the daemon's JobId and sets its status to Done,
// both in memory and in the database. Used when the resync controller
// detects that a daemon's remote job manager has exited.
func (sd *SessionDaemonManager) MarkDone(ctx context.Context, daemonId string) {
	sd.Lock.Lock()
	daemon, ok := sd.Daemons[daemonId]
	sd.Lock.Unlock()
	if !ok {
		return
	}
	daemon.Lock.Lock()
	oldJobId := daemon.JobId
	daemon.JobId = ""
	daemon.Lock.Unlock()
	if err := wstore.DBUpdateFn(ctx, daemonId, func(dbSd *waveobj.SessionDaemon) {
		dbSd.JobId = ""
		dbSd.Status = Status_Done
	}); err != nil {
		// Roll back memory to avoid inconsistency.
		daemon.Lock.Lock()
		daemon.JobId = oldJobId
		daemon.Lock.Unlock()
		log.Printf("[sessiondaemon:%s] MarkDone: DB update failed, rolled back memory JobId: %v", daemonId, err)
		return
	}
	log.Printf("[sessiondaemon:%s] MarkDone: job cleared, status=done", daemonId)
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

// ClearJobIdFromDaemons clears the JobId from all daemons (memory + DB)
// whose job matches jobId. Called when a remote job manager exits.
func (sd *SessionDaemonManager) ClearJobIdFromDaemons(ctx context.Context, jobId string) {
	sd.Lock.Lock()
	var affectedDaemonIds []string
	for _, daemon := range sd.Daemons {
		daemon.Lock.Lock()
		if daemon.JobId == jobId {
			oldDaemonJobId := daemon.JobId
			daemonId := daemon.DaemonId
			daemon.JobId = ""
			daemon.Lock.Unlock()
			if err := wstore.DBUpdateFn(ctx, daemonId, func(dbSd *waveobj.SessionDaemon) {
				dbSd.JobId = ""
				dbSd.Status = Status_Done
			}); err != nil {
				log.Printf("[sessiondaemon:%s] ClearJobIdFromDaemons: DB update failed, memory stale (was job=%s): %v",
					daemonId, oldDaemonJobId, err)
			}
			affectedDaemonIds = append(affectedDaemonIds, daemonId)
			log.Printf("[sessiondaemon:%s] ClearJobIdFromDaemons: job=%s cleared, status=done", daemonId, jobId)
			continue
		}
		daemon.Lock.Unlock()
	}
	sd.Lock.Unlock()

	for _, daemonId := range affectedDaemonIds {
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

		// Read JobManagerPid from the job record.
		job, err := wstore.DBMustGet[*waveobj.Job](ctx, dbDaemon.JobId)
		if err != nil || job.JobManagerPid == 0 {
			continue
		}

		alive, err := conncontroller.CheckRemoteProcessAlive(ctx, connName, job.JobManagerPid)
		if err != nil {
			log.Printf("[sessiondaemon:%s] OnConnectionUp: error checking remote process: %v", dbDaemon.OID, err)
			continue
		}
		if alive {
			// Job manager is still running — try to reconnect and bring
			// it back to running status.
			log.Printf("[sessiondaemon:%s] OnConnectionUp: remote job manager alive (pid=%d), reconnecting", dbDaemon.OID, job.JobManagerPid)
			sd.Lock.Lock()
			memDaemon := sd.Daemons[dbDaemon.OID]
			sd.Lock.Unlock()
			if memDaemon != nil {
				err := memDaemon.Reconnect(ctx, dbDaemon, nil)
				if err != nil {
					log.Printf("[sessiondaemon:%s] OnConnectionUp: reconnect failed: %v", dbDaemon.OID, err)
				}
			}
			continue
		}
		// Job manager is dead.
		log.Printf("[sessiondaemon:%s] OnConnectionUp: remote job manager dead (pid=%d)", dbDaemon.OID, job.JobManagerPid)
		sd.Lock.Lock()
		memDaemon := sd.Daemons[dbDaemon.OID]
		sd.Lock.Unlock()
		hasBlocks := memDaemon != nil && memDaemon.HasAttachedBlocks()

		if hasBlocks {
			if memDaemon != nil {
				memDaemon.Lock.Lock()
				memDaemon.JobId = ""
				memDaemon.Lock.Unlock()
			}
			wstore.DBUpdateFn(ctx, dbDaemon.OID, func(dbSd *waveobj.SessionDaemon) {
				dbSd.JobId = ""
				dbSd.Status = Status_Done
			})
			log.Printf("[sessiondaemon:%s] OnConnectionUp: dead, has blocks, status -> done", dbDaemon.OID)
			if OnDaemonJobDoneFn != nil {
				OnDaemonJobDoneFn(ctx, dbDaemon.OID)
			}
		} else {
			// No blocks referencing this daemon — safe to delete.
			if memDaemon != nil {
				sd.Remove(dbDaemon.OID)
			}
			if err := wstore.DBDelete(ctx, waveobj.OType_SessionDaemon, dbDaemon.OID); err != nil {
				log.Printf("[sessiondaemon:%s] OnConnectionUp: error deleting dead daemon: %v", dbDaemon.OID, err)
			} else {
				log.Printf("[sessiondaemon:%s] OnConnectionUp: dead, no blocks, deleted", dbDaemon.OID)
			}
		}
	}
}

func (sd *SessionDaemonManager) StartIdleReaper(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(IdleCheckInterval * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sd.reapIdleDaemons(ctx)
				sd.verifyConsistency(ctx)
			}
		}
	}()
}

// cleanupDeadBlocks removes block IDs from the daemon's in-memory
// Blocks map that no longer exist in the database. This handles the
// case where a block was deleted without calling DetachBlock.
func (sd *SessionDaemonManager) cleanupDeadBlocks(ctx context.Context, daemonId string, memDaemon *SessionDaemon) {
	// Collect block IDs under the daemon lock, then release it for DB queries.
	memDaemon.Lock.Lock()
	blockIds := make([]string, 0, len(memDaemon.Blocks))
	for blockId := range memDaemon.Blocks {
		blockIds = append(blockIds, blockId)
	}
	memDaemon.Lock.Unlock()

	var deadBlocks []string
	for _, blockId := range blockIds {
		_, err := wstore.DBMustGet[*waveobj.Block](ctx, blockId)
		if err != nil {
			deadBlocks = append(deadBlocks, blockId)
		}
	}

	if len(deadBlocks) == 0 {
		return
	}

	log.Printf("[sessiondaemon] cleanupDeadBlocks: daemon=%s removing %d dead blocks: %v", daemonId, len(deadBlocks), deadBlocks)

	memDaemon.Lock.Lock()
	for _, blockId := range deadBlocks {
		delete(memDaemon.Blocks, blockId)
	}
	remaining := len(memDaemon.Blocks)
	memDaemon.Lock.Unlock()

	if remaining == 0 {
		sd.startIdleCountdown(ctx, daemonId)
	}
}

func (sd *SessionDaemonManager) reapIdleDaemons(ctx context.Context) {
	allDaemons, err := wstore.DBGetAllObjsByType[*waveobj.SessionDaemon](ctx, waveobj.OType_SessionDaemon)
	if err != nil {
		return
	}

	for _, dbDaemon := range allDaemons {
		sd.Lock.Lock()
		memDaemon, hasMem := sd.Daemons[dbDaemon.OID]
		sd.Lock.Unlock()

		switch dbDaemon.Status {
		case Status_Running:
			sd.reapRunning(ctx, dbDaemon, memDaemon, hasMem)
		case Status_Done:
			sd.reapDone(ctx, dbDaemon, memDaemon, hasMem)
		}
	}
}

func (sd *SessionDaemonManager) reapRunning(ctx context.Context, dbDaemon *waveobj.SessionDaemon, memDaemon *SessionDaemon, hasMem bool) {
	if hasMem && memDaemon.HasAttachedBlocks() {
		sd.cleanupDeadBlocks(ctx, dbDaemon.OID, memDaemon)
		if memDaemon.HasAttachedBlocks() {
			return
		}
	}

	if dbDaemon.IdleTimeout <= 0 {
		return
	}

	remaining := sd.advanceIdleTimer(ctx, dbDaemon.OID)
	if remaining > 0 {
		return
	}

	log.Printf("[sessiondaemon:%s] idle timeout reached, terminating", dbDaemon.OID)
	if hasMem {
		err := memDaemon.Stop(ctx)
		if err != nil {
			log.Printf("[sessiondaemon:%s] error stopping daemon, will retry next cycle: %v", dbDaemon.OID, err)
			return
		}
		sd.Remove(dbDaemon.OID)
	}
	if err := wstore.DBDelete(ctx, waveobj.OType_SessionDaemon, dbDaemon.OID); err != nil {
		log.Printf("[sessiondaemon:%s] reapRunning: error deleting from DB: %v", dbDaemon.OID, err)
	}
}

func (sd *SessionDaemonManager) reapDone(ctx context.Context, dbDaemon *waveobj.SessionDaemon, memDaemon *SessionDaemon, hasMem bool) {
	if hasMem && memDaemon.HasAttachedBlocks() {
		return
	}

	if dbDaemon.IdleTimeout <= 0 {
		return
	}

	if dbDaemon.IdleSince <= 0 {
		if err := wstore.DBUpdateFn(ctx, dbDaemon.OID, func(dbD *waveobj.SessionDaemon) {
			dbD.IdleSince = DoneReapTimeout
		}); err != nil {
			log.Printf("[sessiondaemon:%s] reapDone: error setting done reap timeout: %v", dbDaemon.OID, err)
		}
		return
	}

	remaining := sd.advanceIdleTimer(ctx, dbDaemon.OID)
	if remaining > 0 {
		return
	}

	log.Printf("[sessiondaemon:%s] done daemon reaped", dbDaemon.OID)
	if hasMem {
		sd.Remove(dbDaemon.OID)
	}
	if err := wstore.DBDelete(ctx, waveobj.OType_SessionDaemon, dbDaemon.OID); err != nil {
		log.Printf("[sessiondaemon:%s] reapDone: error deleting from DB: %v", dbDaemon.OID, err)
	}
}

func (sd *SessionDaemonManager) verifyConsistency(ctx context.Context) {
	daemons, err := wstore.DBGetAllObjsByType[*waveobj.SessionDaemon](ctx, waveobj.OType_SessionDaemon)
	if err != nil {
		return
	}

	dbIds := make(map[string]bool)
	for _, dbDaemon := range daemons {
		dbIds[dbDaemon.OID] = true
	}

	sd.Lock.Lock()
	defer sd.Lock.Unlock()

	for id := range sd.Daemons {
		if !dbIds[id] {
			log.Printf("[sessiondaemon] consistency: daemon %s in memory but not in DB, removing from memory", id)
			delete(sd.Daemons, id)
		}
	}

	for _, dbDaemon := range daemons {
		if _, exists := sd.Daemons[dbDaemon.OID]; !exists {
			log.Printf("[sessiondaemon] consistency: daemon %s in DB but not in memory, loading", dbDaemon.OID)
			sd.Daemons[dbDaemon.OID] = &SessionDaemon{
				DaemonId:       dbDaemon.OID,
				Name:           dbDaemon.Name,
				JobId:          dbDaemon.JobId,
				InputSessionId: uuid.New().String(),
				Blocks:         make(map[string]bool),
			}
		}
	}
}
