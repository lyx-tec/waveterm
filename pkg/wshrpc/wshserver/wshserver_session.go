// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package wshserver

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/wavetermdev/waveterm/pkg/blockcontroller"
	"github.com/wavetermdev/waveterm/pkg/remote/conncontroller"
	"github.com/wavetermdev/waveterm/pkg/sessiondaemon"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wcore"
	"github.com/wavetermdev/waveterm/pkg/wshrpc"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

func (ws *WshServer) SessionCreateCommand(ctx context.Context, data wshrpc.CommandSessionCreateData) (*wshrpc.SessionInfoRtnData, error) {
	dbDaemon := &waveobj.SessionDaemon{
		OID:         uuid.New().String(),
		Name:        data.Name,
		Connection:  data.Connection,
		IsAnonymous: data.Name == "",
		Status:      sessiondaemon.Status_Init,
		CreatedAt:   time.Now().UnixMilli(),
		IdleTimeout: data.IdleTimeout,
	}
	if dbDaemon.IsAnonymous {
		dbDaemon.IdleTimeout = sessiondaemon.DefaultAnonymousIdleTimeout
	} else if dbDaemon.IdleTimeout <= 0 {
		dbDaemon.IdleTimeout = sessiondaemon.DefaultNamedIdleTimeout
	}

	err := wstore.DBInsert(ctx, dbDaemon)
	if err != nil {
		return nil, fmt.Errorf("insert session daemon: %w", err)
	}

	_, err = sessiondaemon.Manager.GetOrCreate(ctx, dbDaemon)
	if err != nil {
		return nil, fmt.Errorf("create session daemon in manager: %w", err)
	}

	return buildSessionInfoRtnData(ctx, dbDaemon)
}

func (ws *WshServer) SessionDeleteCommand(ctx context.Context, data wshrpc.CommandSessionDeleteData) error {
	dbDaemon, err := wstore.DBMustGet[*waveobj.SessionDaemon](ctx, data.DaemonId)
	if err != nil {
		return fmt.Errorf("session daemon %q not found: %w", data.DaemonId, err)
	}

	memDaemon := sessiondaemon.Manager.Get(data.DaemonId)
	forceDelete := false
	if memDaemon != nil {
		err = memDaemon.Stop(ctx)
		if err != nil {
			forceDelete = isRemoteProcessDead(ctx, dbDaemon)
			if !forceDelete {
				return fmt.Errorf("failed to stop session daemon: %w", err)
			}
			log.Printf("[sessiondaemon] SessionDelete: daemon=%s remote job dead, deleting despite stop failure", data.DaemonId)
		}
		sessiondaemon.Manager.Remove(data.DaemonId)
	}

	err = wstore.DBDelete(ctx, waveobj.OType_SessionDaemon, data.DaemonId)
	if err != nil {
		return fmt.Errorf("delete session daemon: %w", err)
	}
	return nil
}

func isRemoteProcessDead(ctx context.Context, dbDaemon *waveobj.SessionDaemon) bool {
	if dbDaemon.JobId == "" {
		return false
	}
	job, err := wstore.DBMustGet[*waveobj.Job](ctx, dbDaemon.JobId)
	if err != nil || job.JobManagerPid == 0 {
		return false
	}
	alive, err := conncontroller.CheckRemoteProcessAlive(ctx, dbDaemon.Connection, job.JobManagerPid)
	return err == nil && !alive
}

func (ws *WshServer) SessionListCommand(ctx context.Context, data wshrpc.CommandSessionListData) ([]wshrpc.SessionInfoRtnData, error) {
	allDaemons, err := wstore.DBGetAllObjsByType[*waveobj.SessionDaemon](ctx, waveobj.OType_SessionDaemon)
	if err != nil {
		return nil, fmt.Errorf("list session daemons: %w", err)
	}

	rtn := make([]wshrpc.SessionInfoRtnData, 0)
	for _, dbDaemon := range allDaemons {
		if dbDaemon.IsAnonymous && !data.ShowAll {
			continue
		}
		info, err := buildSessionInfoRtnData(ctx, dbDaemon)
		if err != nil {
			return nil, err
		}
		rtn = append(rtn, *info)
	}
	sort.Slice(rtn, func(i, j int) bool {
		ai := rtn[i].LastActiveAt
		aj := rtn[j].LastActiveAt
		if ai != aj {
			return ai > aj
		}
		return rtn[i].CreatedAt > rtn[j].CreatedAt
	})
	return rtn, nil
}

func (ws *WshServer) SessionAttachCommand(ctx context.Context, data wshrpc.CommandSessionAttachData) error {
	if data.CurrentDaemonId != "" && data.CurrentDaemonId == data.DaemonId {
		return nil
	}

	if data.CurrentDaemonId != "" {
		sessiondaemon.Manager.DetachBlock(ctx, data.CurrentDaemonId, data.BlockId)
	}

	dbDaemon, err := wstore.DBMustGet[*waveobj.SessionDaemon](ctx, data.DaemonId)
	if err != nil {
		if data.CurrentDaemonId != "" {
			sessiondaemon.Manager.AttachBlock(ctx, data.CurrentDaemonId, data.BlockId)
		}
		return fmt.Errorf("session daemon %q not found: %w", data.DaemonId, err)
	}

	blockData, err := wstore.DBMustGet[*waveobj.Block](ctx, data.BlockId)
	if err == nil {
		blockConn := blockData.Meta.GetString(waveobj.MetaKey_Connection, "")
		if blockConn != "" && blockConn != dbDaemon.Connection {
			log.Printf("[sessiondaemon] SessionAttach: block=%s conn=%q daemon conn=%q mismatch, refusing",
				data.BlockId, blockConn, dbDaemon.Connection)
			return fmt.Errorf("cannot attach to session on connection %q from connection %q", dbDaemon.Connection, blockConn)
		}
	}

	_, err = sessiondaemon.Manager.GetOrCreate(ctx, dbDaemon)
	if err != nil {
		return fmt.Errorf("create session daemon in manager: %w", err)
	}

	sessiondaemon.Manager.AttachBlock(ctx, data.DaemonId, data.BlockId)

	err = wstore.DBUpdateFn(ctx, data.BlockId, func(block *waveobj.Block) {
		block.Meta[waveobj.MetaKey_SessionDaemonId] = data.DaemonId
		delete(block.Meta, blockcontroller.MetaKey_SessionNoAutoCreate)
		block.JobId = dbDaemon.JobId
	})

	if err != nil {
		sessiondaemon.Manager.DetachBlock(ctx, data.DaemonId, data.BlockId)
		if data.CurrentDaemonId != "" {
			sessiondaemon.Manager.AttachBlock(ctx, data.CurrentDaemonId, data.BlockId)
		}
		return fmt.Errorf("update block meta: %w", err)
	}

	resyncBlockController(ctx, data.BlockId)
	wcore.SendWaveObjUpdate(waveobj.MakeORef(waveobj.OType_Block, data.BlockId))
	return nil
}

func (ws *WshServer) SessionDetachCommand(ctx context.Context, data wshrpc.CommandSessionDetachData) error {
	_, err := wstore.DBMustGet[*waveobj.SessionDaemon](ctx, data.DaemonId)
	if err != nil {
		return fmt.Errorf("session daemon %q not found: %w", data.DaemonId, err)
	}

	blockIds := []string{}
	if data.BlockId != "" {
		blockIds = append(blockIds, data.BlockId)
	} else {
		blockIds = sessiondaemon.Manager.GetBlocksForDaemon(data.DaemonId)
	}

	for _, blockId := range blockIds {
		sessiondaemon.Manager.DetachBlock(ctx, data.DaemonId, blockId)
		err = wstore.DBUpdateFn(ctx, blockId, func(block *waveobj.Block) {
			delete(block.Meta, waveobj.MetaKey_SessionDaemonId)
			block.Meta[blockcontroller.MetaKey_SessionNoAutoCreate] = true
		})
		if err != nil {
			return fmt.Errorf("update block meta: %w", err)
		}
		resyncBlockController(ctx, blockId)
		wcore.SendWaveObjUpdate(waveobj.MakeORef(waveobj.OType_Block, blockId))
	}
	return nil
}

func (ws *WshServer) SessionInfoCommand(ctx context.Context, data wshrpc.CommandSessionInfoData) (*wshrpc.SessionInfoRtnData, error) {
	dbDaemon, err := wstore.DBMustGet[*waveobj.SessionDaemon](ctx, data.DaemonId)
	if err != nil {
		return nil, fmt.Errorf("session daemon %q not found: %w", data.DaemonId, err)
	}
	if dbDaemon.JobId == "" {
		if memJobId := sessiondaemon.Manager.GetMemJobId(dbDaemon.OID); memJobId != "" {
			dbDaemon.JobId = memJobId
		}
	}
	return buildSessionInfoRtnData(ctx, dbDaemon)
}

func (ws *WshServer) SessionTagCommand(ctx context.Context, data wshrpc.CommandSessionTagData) error {
	_, err := wstore.DBMustGet[*waveobj.SessionDaemon](ctx, data.DaemonId)
	if err != nil {
		return fmt.Errorf("session daemon %q not found: %w", data.DaemonId, err)
	}
	return sessiondaemon.Manager.Rename(ctx, data.DaemonId, data.Name)
}

func (ws *WshServer) RecordSessionActivityCommand(ctx context.Context, data wshrpc.CommandRecordSessionActivityData) error {
	return sessiondaemon.Manager.RecordActivity(ctx, data.DaemonId)
}

func buildSessionInfoRtnData(ctx context.Context, dbDaemon *waveobj.SessionDaemon) (*wshrpc.SessionInfoRtnData, error) {
	if dbDaemon == nil {
		return nil, fmt.Errorf("session daemon is nil")
	}
	blocks := sessiondaemon.Manager.GetBlocksForDaemon(dbDaemon.OID)
	return &wshrpc.SessionInfoRtnData{
		DaemonId:     dbDaemon.OID,
		Name:         dbDaemon.Name,
		Connection:   dbDaemon.Connection,
		JobId:        dbDaemon.JobId,
		IsAnonymous:  dbDaemon.IsAnonymous,
		Status:       dbDaemon.Status,
		Cwd:          dbDaemon.Cwd,
		CreatedAt:    dbDaemon.CreatedAt,
		IdleTimeout:  dbDaemon.IdleTimeout,
		IdleSince:    dbDaemon.IdleSince,
		LastActiveAt: dbDaemon.LastActiveAt,
		Blocks:       blocks,
	}, nil
}

func resyncBlockController(ctx context.Context, blockId string) {
	tabs, err := wstore.DBGetAllObjsByType[*waveobj.Tab](ctx, waveobj.OType_Tab)
	if err != nil {
		log.Printf("[sessiondaemon] warning: error getting tabs for resync: %v", err)
		return
	}
	for _, tab := range tabs {
		for _, bid := range tab.BlockIds {
			if bid == blockId {
				blockcontroller.ResyncController(ctx, tab.OID, blockId, nil, true)
				return
			}
		}
	}
}
