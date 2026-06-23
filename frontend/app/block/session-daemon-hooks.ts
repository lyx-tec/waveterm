// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { globalStore } from "@/app/store/jotaiStore";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { useWaveEnv } from "@/app/waveenv/waveenv";
import { fireAndForget } from "@/util/util";
import { autoUpdate, flip, offset, shift, useFloating } from "@floating-ui/react";
import * as jotai from "jotai";
import type * as React from "react";
import { Dispatch, SetStateAction, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { BlockEnv } from "./blockenv";
import { SessionDisplayData, SessionInfo } from "./session-daemon-types";

const EmptySessionDisplayAtom = jotai.atom<SessionDisplayData>({ name: null, isanonymous: true });
const sessionDisplayAtomMap = new Map<string, jotai.PrimitiveAtom<SessionDisplayData>>();

function getSessionDisplayAtom(daemonId: string): jotai.PrimitiveAtom<SessionDisplayData> {
    let a = sessionDisplayAtomMap.get(daemonId);
    if (!a) {
        a = jotai.atom<SessionDisplayData>({ name: null, isanonymous: true });
        sessionDisplayAtomMap.set(daemonId, a);
    }
    return a;
}

export interface SessionDaemonIndicatorState {
    daemonId: string;
    visible: boolean;
    showPopup: boolean;
    setShowPopup: Dispatch<SetStateAction<boolean>>;
    sessions: SessionInfo[];
    sameConnSessions: SessionInfo[];
    sessionDisplay: SessionDisplayData;
    editingId: string;
    editName: string;
    setEditName: Dispatch<SetStateAction<string>>;
    creating: boolean;
    showCreateInput: boolean;
    setShowCreateInput: Dispatch<SetStateAction<boolean>>;
    newSessionName: string;
    setNewSessionName: Dispatch<SetStateAction<string>>;
    popupRef: React.RefObject<HTMLDivElement>;
    iconRef: React.RefObject<HTMLDivElement>;
    editInputRef: React.RefObject<HTMLInputElement>;
    createInputRef: React.RefObject<HTMLInputElement>;
    floatingStyles: React.CSSProperties;
    handleAttach: (targetDaemonId: string) => void;
    handleStartEdit: (daemonId: string, currentName: string) => void;
    handleSaveEdit: () => void;
    handleCancelEdit: () => void;
    handleCreateAndAttach: (name?: string) => Promise<void>;
    handleDelete: (daemonId: string) => void;
}

export function useSessionDaemonIndicator(blockId: string): SessionDaemonIndicatorState {
    const waveEnv = useWaveEnv<BlockEnv>();
    const daemonId = jotai.useAtomValue(waveEnv.getBlockMetaKeyAtom(blockId, "session:daemonid"));
    const connName = jotai.useAtomValue(waveEnv.getBlockMetaKeyAtom(blockId, "connection"));
    const [showPopup, setShowPopup] = useState(false);
    const [sessions, setSessions] = useState<SessionInfo[]>([]);
    const [editingId, setEditingId] = useState<string | null>(null);
    const [editName, setEditName] = useState("");
    const [creating, setCreating] = useState(false);
    const creatingRef = useRef(false);
    const [showCreateInput, setShowCreateInput] = useState(false);
    const [newSessionName, setNewSessionName] = useState("");
    const createInputRef = useRef<HTMLInputElement>(null);
    const editInputRef = useRef<HTMLInputElement>(null);
    const popupRef = useRef<HTMLDivElement>(null);
    const iconRef = useRef<HTMLDivElement>(null);
    const sessionDisplayAtom = daemonId ? getSessionDisplayAtom(daemonId) : EmptySessionDisplayAtom;
    const sessionDisplay = jotai.useAtomValue(sessionDisplayAtom);
    const isSshConn = connName && !connName.startsWith("local") && !connName.startsWith("wsl://");
    const visible = !!daemonId || isSshConn;
    const sameConnSessions = useMemo(() => sessions.filter((s) => s.connection === connName), [sessions, connName]);
    const { floatingStyles } = useFloating({
        elements: {
            reference: iconRef.current,
            floating: popupRef.current,
        },
        open: showPopup,
        onOpenChange: setShowPopup,
        placement: "bottom-end",
        middleware: [offset(6), flip(), shift({ padding: 12 })],
        whileElementsMounted: autoUpdate,
    });

    useEffect(() => {
        if (!showPopup) return;
        fireAndForget(async () => {
            try {
                const list = await RpcApi.SessionListCommand(TabRpcClient, { showall: true });
                setSessions((list ?? []) as SessionInfo[]);
            } catch (e) {
                console.log("error loading session list:", e);
            }
        });
    }, [showPopup]);

    useEffect(() => {
        if (!daemonId) return;
        fireAndForget(async () => {
            try {
                const info = await RpcApi.SessionInfoCommand(TabRpcClient, { daemonid: daemonId });
                if (info) {
                    const atom = getSessionDisplayAtom(daemonId);
                    globalStore.set(atom, { name: info.name || null, isanonymous: info.isanonymous });
                }
            } catch (_) {}
        });
    }, [daemonId]);

    useEffect(() => {
        function handleClick(e: MouseEvent) {
            if (
                popupRef.current &&
                !popupRef.current.contains(e.target as Node) &&
                iconRef.current &&
                !iconRef.current.contains(e.target as Node)
            ) {
                setShowPopup(false);
            }
        }
        if (showPopup) {
            document.addEventListener("mousedown", handleClick);
            return () => document.removeEventListener("mousedown", handleClick);
        }
    }, [showPopup]);

    const handleAttach = useCallback((targetDaemonId: string) => {
        if (targetDaemonId === daemonId) return;
        if (editingId) return;
        fireAndForget(async () => {
            try {
                await RpcApi.SessionAttachCommand(TabRpcClient, { daemonid: targetDaemonId, blockid: blockId, currentdaemonid: daemonId ?? undefined });
                setShowPopup(false);
            } catch (e) {
                console.log("error switching session:", e);
            }
        });
    }, [daemonId, editingId, blockId]);

    const handleStartEdit = useCallback((daemonId: string, currentName: string) => {
        setEditingId(daemonId);
        setEditName(currentName || "");
        setTimeout(() => editInputRef.current?.focus(), 0);
    }, []);

    const handleSaveEdit = useCallback(() => {
        const id = editingId;
        const name = editName.trim();
        if (!id) return;
        setEditingId(null);
        const atom = getSessionDisplayAtom(id);
        globalStore.set(atom, { name: name || null, isanonymous: !name });
        fireAndForget(async () => {
            try {
                await RpcApi.SessionTagCommand(TabRpcClient, { daemonid: id, name: name || "Unnamed session" });
                const list = await RpcApi.SessionListCommand(TabRpcClient, { showall: true });
                setSessions((list ?? []) as SessionInfo[]);
            } catch (e) {
                console.log("error renaming session:", e);
            }
        });
    }, [editingId, editName]);

    const handleCancelEdit = useCallback(() => {
        setEditingId(null);
    }, []);

    const handleCreateAndAttach = useCallback(async (name?: string) => {
        if (!connName || creatingRef.current) return;
        creatingRef.current = true;
        setCreating(true);
        try {
            const info = await RpcApi.SessionCreateCommand(TabRpcClient, { connection: connName, name });
            if (info?.daemonid) {
                await RpcApi.SessionAttachCommand(TabRpcClient, {
                    daemonid: info.daemonid,
                    blockid: blockId,
                    currentdaemonid: daemonId ?? undefined,
                });
                setShowPopup(false);
            }
        } catch (e) {
            console.log("error creating session:", e);
        } finally {
            creatingRef.current = false;
            setCreating(false);
        }
    }, [connName, blockId, daemonId]);

    const handleDelete = useCallback((daemonId: string) => {
        fireAndForget(async () => {
            try {
                await RpcApi.SessionDeleteCommand(TabRpcClient, { daemonid: daemonId });
                setSessions((prev) => prev.filter((x) => x.daemonid !== daemonId));
            } catch (e) {
                console.log("error closing session:", e);
            }
        });
    }, []);

    return {
        daemonId,
        visible,
        showPopup,
        setShowPopup,
        sessions,
        sameConnSessions,
        sessionDisplay,
        editingId,
        editName,
        setEditName,
        creating,
        showCreateInput,
        setShowCreateInput,
        newSessionName,
        setNewSessionName,
        popupRef,
        iconRef,
        editInputRef,
        createInputRef,
        floatingStyles,
        handleAttach,
        handleStartEdit,
        handleSaveEdit,
        handleCancelEdit,
        handleCreateAndAttach,
        handleDelete,
    };
}
