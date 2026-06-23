package sessiondaemon

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

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

func (sd *SessionDaemonManager) cleanupDeadBlocks(ctx context.Context, daemonId string, memDaemon *SessionDaemon) {
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
