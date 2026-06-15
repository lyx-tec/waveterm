package sessiondaemon

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wavetermdev/waveterm/pkg/jobcontroller"
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

func (sd *SessionDaemon) SetJobId(ctx context.Context, dbDaemon *waveobj.SessionDaemon, jobId string) error {
	sd.Lock.Lock()
	sd.JobId = jobId
	sd.Lock.Unlock()

	err := wstore.DBUpdateFn(ctx, dbDaemon.OID, func(sdDb *waveobj.SessionDaemon) {
		sdDb.JobId = jobId
		sdDb.Status = Status_Running
	})
	if err != nil {
		log.Printf("[sessiondaemon:%s] warning: failed to update jobid in db: %v", sd.DaemonId, err)
	}
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
		wstore.DBUpdateFn(ctx, dbDaemon.OID, func(dbSd *waveobj.SessionDaemon) {
			if dbSd.JobId == "" {
				dbSd.Status = Status_Done
				jobGone = true
			} else {
				dbSd.Status = Status_Disconnected
			}
		})
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

	wstore.DBUpdateFn(ctx, dbDaemon.OID, func(dbSd *waveobj.SessionDaemon) {
		dbSd.Status = Status_Running
	})
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
	// Reset idle countdown: block attached, daemon is no longer idle.
	wstore.DBUpdateFn(ctx, daemonId, func(dbD *waveobj.SessionDaemon) {
		dbD.IdleSince = 0
	})
}

func (sd *SessionDaemonManager) DetachBlock(ctx context.Context, daemonId string, blockId string) {
	stackBuf := make([]byte, 4096)
	stackLen := runtime.Stack(stackBuf, false)
	log.Printf("[sessiondaemon] DetachBlock: daemon=%s block=%s stack:\n%s", daemonId, blockId, string(stackBuf[:stackLen]))
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
		// Start idle countdown (IdleTimeout in seconds).
		// Survives app restart: if daemon was idle before shutdown,
		// it resumes counting down from where it left off.
		wstore.DBUpdateFn(ctx, daemonId, func(dbD *waveobj.SessionDaemon) {
			dbD.IdleSince = dbD.IdleTimeout
		})
	}
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

func (sd *SessionDaemonManager) InitFromDB(ctx context.Context) error {
	daemons, err := wstore.DBGetAllObjsByType[*waveobj.SessionDaemon](ctx, waveobj.OType_SessionDaemon)
	if err != nil {
		return fmt.Errorf("load session daemons: %w", err)
	}

	for _, dbDaemon := range daemons {
		daemon, err := sd.GetOrCreate(ctx, dbDaemon)
		if err != nil {
			log.Printf("[sessiondaemon] warning: failed to load daemon %s: %v", dbDaemon.OID, err)
			continue
		}

		switch dbDaemon.Status {
		case Status_Running, Status_Disconnected:
			err = daemon.Reconnect(ctx, dbDaemon, nil)
			if err != nil {
				log.Printf("[sessiondaemon:%s] reconnect failed: %v", dbDaemon.OID, err)
			}
		case Status_Done:
			log.Printf("[sessiondaemon:%s] loaded done daemon", dbDaemon.OID)
		case Status_Init:
			log.Printf("[sessiondaemon:%s] loaded init daemon", dbDaemon.OID)
		default:
			log.Printf("[sessiondaemon:%s] unknown status %q, treating as init", dbDaemon.OID, dbDaemon.Status)
			wstore.DBUpdateFn(ctx, dbDaemon.OID, func(dbSd *waveobj.SessionDaemon) {
				dbSd.Status = Status_Init
			})
		}
	}

	log.Printf("[sessiondaemon] InitFromDB complete: %d daemons loaded", len(sd.Daemons))
	return nil
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
	memDaemon.Lock.Lock()
	var deadBlocks []string
	for blockId := range memDaemon.Blocks {
		_, err := wstore.DBMustGet[*waveobj.Block](ctx, blockId)
		if err != nil {
			deadBlocks = append(deadBlocks, blockId)
		}
	}
	for _, blockId := range deadBlocks {
		delete(memDaemon.Blocks, blockId)
	}
	memDaemon.Lock.Unlock()

	if len(deadBlocks) > 0 {
		log.Printf("[sessiondaemon] cleanupDeadBlocks: daemon=%s removed %d dead blocks: %v", daemonId, len(deadBlocks), deadBlocks)
		remaining := len(memDaemon.Blocks)
		if remaining == 0 {
			// All blocks are dead, start idle countdown.
			wstore.DBUpdateFn(ctx, daemonId, func(dbD *waveobj.SessionDaemon) {
				dbD.IdleSince = dbD.IdleTimeout
			})
		}
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

	var newRemaining int64
	wstore.DBUpdateFn(ctx, dbDaemon.OID, func(dbD *waveobj.SessionDaemon) {
		dbD.IdleSince -= IdleCheckInterval
		newRemaining = dbD.IdleSince
	})
	if newRemaining > 0 {
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
	wstore.DBDelete(ctx, waveobj.OType_SessionDaemon, dbDaemon.OID)
}

func (sd *SessionDaemonManager) reapDone(ctx context.Context, dbDaemon *waveobj.SessionDaemon, memDaemon *SessionDaemon, hasMem bool) {
	if hasMem && memDaemon.HasAttachedBlocks() {
		return
	}

	if dbDaemon.IdleTimeout <= 0 {
		return
	}

	if dbDaemon.IdleSince <= 0 {
		wstore.DBUpdateFn(ctx, dbDaemon.OID, func(dbD *waveobj.SessionDaemon) {
			dbD.IdleSince = DoneReapTimeout
		})
		return
	}

	var newRemaining int64
	wstore.DBUpdateFn(ctx, dbDaemon.OID, func(dbD *waveobj.SessionDaemon) {
		dbD.IdleSince -= IdleCheckInterval
		newRemaining = dbD.IdleSince
	})
	if newRemaining > 0 {
		return
	}

	log.Printf("[sessiondaemon:%s] done daemon reaped", dbDaemon.OID)
	if hasMem {
		sd.Remove(dbDaemon.OID)
	}
	wstore.DBDelete(ctx, waveobj.OType_SessionDaemon, dbDaemon.OID)
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
