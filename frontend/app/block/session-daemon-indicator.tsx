// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { useWaveEnv } from "@/app/waveenv/waveenv";
import { fireAndForget } from "@/util/util";
import { autoUpdate, flip, FloatingPortal, offset, shift, useFloating } from "@floating-ui/react";
import * as jotai from "jotai";
import { useEffect, useRef, useState } from "react";
import { BlockEnv } from "./blockenv";

function formatCreatedTime(ms: number): string {
    const d = new Date(ms);
    const now = new Date();
    const diffMs = now.getTime() - d.getTime();
    const diffMin = Math.floor(diffMs / 60000);
    if (diffMin < 1) return "just now";
    if (diffMin < 60) return `${diffMin}m ago`;
    const diffHr = Math.floor(diffMin / 60);
    if (diffHr < 24) return `${diffHr}h ago`;
    const diffDay = Math.floor(diffHr / 24);
    if (diffDay < 7) return `${diffDay}d ago`;
    return d.toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
}

interface SessionInfo {
    daemonid: string;
    name: string;
    connection: string;
    status: string;
    isanonymous: boolean;
    createdat?: number;
    blocks?: string[];
    jobid?: string;
}

interface SessionDaemonIndicatorProps {
    blockId: string;
    useTermHeader: boolean;
}

const popupStyle = {
    zIndex: 100,
    width: "min(420px, calc(100vw - 24px))",
    maxHeight: 360,
    overflowY: "auto",
    background: "color-mix(in srgb, var(--bg-secondary, #1e1e2e) 96%, black)",
    border: "1px solid color-mix(in srgb, var(--border-primary, #45475a) 78%, transparent)",
    borderRadius: 10,
    padding: 8,
    boxShadow: "0 18px 42px rgba(0,0,0,0.42), 0 2px 8px rgba(0,0,0,0.28)",
} as const;

const truncateStyle = {
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
} as const;

function SessionStatusPill({ status }: { status: string }) {
    const isRunning = status === "running";
    return (
        <span
            style={{
                flexShrink: 0,
                display: "inline-flex",
                alignItems: "center",
                gap: 5,
                height: 20,
                padding: "0 8px",
                borderRadius: 999,
                fontSize: 11,
                lineHeight: "20px",
                color: isRunning ? "#4ade80" : "var(--text-muted)",
                background: isRunning ? "rgba(74, 222, 128, 0.12)" : "rgba(148, 163, 184, 0.10)",
                border: `1px solid ${isRunning ? "rgba(74, 222, 128, 0.22)" : "rgba(148, 163, 184, 0.14)"}`,
            }}
        >
            <span
                style={{
                    width: 6,
                    height: 6,
                    borderRadius: 999,
                    background: isRunning ? "#4ade80" : "var(--text-muted)",
                    boxShadow: isRunning ? "0 0 8px rgba(74, 222, 128, 0.7)" : "none",
                }}
            />
            {status || "unknown"}
        </span>
    );
}

export function SessionDaemonIndicator({ blockId, useTermHeader }: SessionDaemonIndicatorProps) {
    const waveEnv = useWaveEnv<BlockEnv>();
    const daemonId = jotai.useAtomValue(waveEnv.getBlockMetaKeyAtom(blockId, "session:daemonid"));
    const [showPopup, setShowPopup] = useState(false);
    const [sessions, setSessions] = useState<SessionInfo[]>([]);
    const popupRef = useRef<HTMLDivElement>(null);
    const iconRef = useRef<HTMLDivElement>(null);
    const { refs, floatingStyles } = useFloating({
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

    const handleAttach = (targetDaemonId: string) => {
        if (targetDaemonId === daemonId) return;
        fireAndForget(async () => {
            try {
                await RpcApi.SessionAttachCommand(TabRpcClient, { daemonid: targetDaemonId, blockid: blockId, currentdaemonid: daemonId ?? undefined });
                setShowPopup(false);
            } catch (e) {
                console.log("error switching session:", e);
            }
        });
    };

    if (!useTermHeader) {
        return null;
    }

    return (
        <>
            <div
                ref={(elem) => {
                    iconRef.current = elem;
                    refs.setReference(elem);
                }}
                className="iconbutton text-[13px] ml-[-4px]"
                title={daemonId ? `Session: ${daemonId}` : "Attach to Session"}
                onClick={() => setShowPopup((v) => !v)}
                style={{ display: "inline-flex", alignItems: "center", gap: 4 }}
            >
                <i className={`fa-sharp fa-solid ${daemonId ? "fa-link text-sky-500" : "fa-link-slash text-muted"}`} />
                {daemonId && (
                    <span style={{ fontSize: 11, color: "var(--text-muted)", maxWidth: 72, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                        {daemonId.slice(0, 8)}
                    </span>
                )}
            </div>
            {showPopup && (
                <FloatingPortal>
                    <div
                        ref={(elem) => {
                            popupRef.current = elem;
                            refs.setFloating(elem);
                        }}
                        style={{ ...popupStyle, ...floatingStyles }}
                        onMouseDown={(e) => e.stopPropagation()}
                        onFocusCapture={(e) => e.stopPropagation()}
                        onClick={(e) => e.stopPropagation()}
                    >
                        <div
                            style={{
                                display: "flex",
                                alignItems: "center",
                                justifyContent: "space-between",
                                padding: "4px 6px 8px",
                                borderBottom: "1px solid rgba(148, 163, 184, 0.12)",
                                marginBottom: 4,
                            }}
                        >
                            <div style={{ display: "flex", alignItems: "center", gap: 7, minWidth: 0 }}>
                                <i className="fa-sharp fa-solid fa-link" style={{ color: "#38bdf8", fontSize: 12 }} />
                                <span style={{ fontSize: 12, fontWeight: 600, color: "var(--text-primary)" }}>
                                    Sessions
                                </span>
                            </div>
                            <span
                                style={{
                                    flexShrink: 0,
                                    fontSize: 11,
                                    color: "var(--text-muted)",
                                    background: "rgba(148, 163, 184, 0.10)",
                                    border: "1px solid rgba(148, 163, 184, 0.12)",
                                    borderRadius: 999,
                                    padding: "1px 7px",
                                }}
                            >
                                {sessions.length}
                            </span>
                        </div>
                        {sessions.length === 0 && (
                            <div style={{ fontSize: 12, color: "var(--text-muted)", padding: "14px 10px" }}>
                                Loading sessions...
                            </div>
                        )}
                        {sessions.map((s) => {
                            const isActive = s.daemonid === daemonId;
                            const blockCount = s.blocks?.length ?? 0;
                            const canClose = blockCount === 0;
                            const displayStatus = blockCount === 0 ? "idle" : s.status;
                            return (
                                <div
                                    key={s.daemonid}
                                    onClick={() => handleAttach(s.daemonid)}
                                    title={`${s.name || s.connection} · ${s.status}`}
                                    style={{
                                        display: "grid",
                                        gridTemplateColumns: "minmax(0, 1fr) auto",
                                        gap: 10,
                                        padding: "9px 10px",
                                        marginTop: 4,
                                        cursor: isActive ? "default" : "pointer",
                                        borderRadius: 8,
                                        fontSize: 13,
                                        background: isActive ? "rgba(56, 189, 248, 0.12)" : "transparent",
                                        border: isActive
                                            ? "1px solid rgba(56, 189, 248, 0.24)"
                                            : "1px solid transparent",
                                    }}
                                    onMouseEnter={(e) => {
                                        if (!isActive) {
                                            e.currentTarget.style.background = "rgba(148, 163, 184, 0.08)";
                                        }
                                    }}
                                    onMouseLeave={(e) => {
                                        if (!isActive) {
                                            e.currentTarget.style.background = "transparent";
                                        }
                                    }}
                                >
                                    <div style={{ display: "flex", gap: 9, minWidth: 0 }}>
                                        <span
                                            style={{
                                                width: 28,
                                                height: 28,
                                                borderRadius: 7,
                                                flexShrink: 0,
                                                display: "inline-flex",
                                                alignItems: "center",
                                                justifyContent: "center",
                                                color: isActive ? "#7dd3fc" : "var(--text-muted)",
                                                background: isActive
                                                    ? "rgba(56, 189, 248, 0.16)"
                                                    : "rgba(148, 163, 184, 0.08)",
                                            }}
                                        >
                                            <i
                                                className={`fa-sharp fa-solid ${s.isanonymous ? "fa-link" : "fa-tag"}`}
                                            />
                                        </span>
                                        <div style={{ minWidth: 0 }}>
                                            <div
                                                style={{
                                                    ...truncateStyle,
                                                    fontWeight: isActive ? 650 : 500,
                                                    color: "var(--text-primary)",
                                                    lineHeight: "18px",
                                                }}
                                            >
                                                {s.name || "Unnamed session"}
                                            </div>
                                            <div
                                                style={{
                                                    ...truncateStyle,
                                                    fontSize: 11,
                                                    color: "var(--text-muted)",
                                                    marginTop: 1,
                                                    fontFamily: "monospace",
                                                }}
                                            >
                                                {s.daemonid.slice(0, 8)}
                                            </div>
                                        </div>
                                    </div>
                                    <div
                                        style={{
                                            display: "flex",
                                            flexDirection: "column",
                                            alignItems: "flex-end",
                                            gap: 5,
                                        }}
                                    >
                                        <SessionStatusPill status={displayStatus} />
                                        <span
                                            style={{ fontSize: 10, color: "var(--text-muted)", opacity: 0.6 }}
                                        >
                                            {formatCreatedTime(s.createdat)}
                                        </span>
                                        {canClose ? (
                                            <span
                                                onClick={(e) => {
                                                    e.stopPropagation();
                                                    fireAndForget(async () => {
                                                        try {
                                                            await RpcApi.SessionDeleteCommand(TabRpcClient, {
                                                                daemonid: s.daemonid,
                                                            });
                                                            setSessions((prev) => prev.filter((x) => x.daemonid !== s.daemonid));
                                                        } catch (e) {
                                                            console.log("error closing session:", e);
                                                        }
                                                    });
                                                }}
                                                style={{
                                                    fontSize: 11,
                                                    color: "var(--text-muted)",
                                                    cursor: "pointer",
                                                    opacity: 0.6,
                                                    display: "inline-flex",
                                                    alignItems: "center",
                                                    gap: 3,
                                                }}
                                                title="Close idle session"
                                            >
                                                <i className="fa-sharp fa-solid fa-xmark" />
                                                Close
                                            </span>
                                        ) : (
                                            <span style={{ fontSize: 10, color: "var(--text-muted)", opacity: 0.6 }}>
                                                {isActive ? "active" : `${blockCount} block${blockCount === 1 ? "" : "s"}`}
                                            </span>
                                        )}
                                    </div>
                                </div>
                            );
                        })}
                    </div>
                </FloatingPortal>
            )}
        </>
    );
}
