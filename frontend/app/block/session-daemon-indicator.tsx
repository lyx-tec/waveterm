// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { useWaveEnv } from "@/app/waveenv/waveenv";
import { fireAndForget } from "@/util/util";
import * as jotai from "jotai";
import { useEffect, useRef, useState } from "react";
import { BlockEnv } from "./blockenv";

interface SessionInfo {
    daemonid: string;
    name: string;
    connection: string;
    status: string;
    isanonymous: boolean;
    blocks?: string[];
    jobid?: string;
}

interface SessionDaemonIndicatorProps {
    blockId: string;
    useTermHeader: boolean;
}

export function SessionDaemonIndicator({ blockId, useTermHeader }: SessionDaemonIndicatorProps) {
    const waveEnv = useWaveEnv<BlockEnv>();
    const daemonId = jotai.useAtomValue(
        waveEnv.getBlockMetaKeyAtom(blockId, "session:daemonid")
    );
    const [showPopup, setShowPopup] = useState(false);
    const [sessions, setSessions] = useState<SessionInfo[]>([]);
    const popupRef = useRef<HTMLDivElement>(null);
    const iconRef = useRef<HTMLDivElement>(null);

    useEffect(() => {
        if (!showPopup) return;
        fireAndForget(async () => {
            try {
                const list = await RpcApi.SessionListCommand(TabRpcClient, { showall: true });
                setSessions(list as SessionInfo[]);
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
                if (daemonId) {
                    await RpcApi.SessionDetachCommand(TabRpcClient, { daemonid: daemonId, blockid: blockId });
                }
                await RpcApi.SessionAttachCommand(TabRpcClient, { daemonid: targetDaemonId, blockid: blockId });
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
        <div style={{ position: "relative" }}>
            <div
                ref={iconRef}
                className="iconbutton text-[13px] ml-[-4px]"
                title={daemonId ? `Session: ${daemonId}` : "Attach to Session"}
                onClick={() => setShowPopup((v) => !v)}
            >
                <i className={`fa-sharp fa-solid ${daemonId ? "fa-link text-sky-500" : "fa-link-slash text-muted"}`} />
            </div>
            {showPopup && (
                <div
                    ref={popupRef}
                    style={{
                        position: "absolute",
                        top: "100%",
                        left: 0,
                        zIndex: 100,
                        minWidth: 300,
                        maxHeight: 300,
                        overflowY: "auto",
                        background: "var(--bg-secondary, #1e1e2e)",
                        border: "1px solid var(--border-primary, #45475a)",
                        borderRadius: 6,
                        padding: 8,
                        boxShadow: "0 4px 12px rgba(0,0,0,0.3)",
                    }}
                >
                    <div style={{ fontSize: 12, color: "var(--text-muted)", marginBottom: 6, padding: "0 4px" }}>
                        Sessions ({sessions.length})
                    </div>
                    {sessions.length === 0 && (
                        <div style={{ fontSize: 12, color: "var(--text-muted)", padding: 8 }}>loading...</div>
                    )}
                    {sessions.map((s) => (
                        <div
                            key={s.daemonid}
                            onClick={() => handleAttach(s.daemonid)}
                            style={{
                                padding: "6px 8px",
                                cursor: s.daemonid === daemonId ? "default" : "pointer",
                                borderRadius: 4,
                                fontSize: 13,
                                background:
                                    s.daemonid === daemonId ? "var(--bg-active, rgba(255,255,255,0.06))" : "transparent",
                                opacity: s.daemonid === daemonId ? 0.7 : 1,
                            }}
                            onMouseEnter={(e) => {
                                if (s.daemonid !== daemonId) {
                                    (e.currentTarget as HTMLElement).style.background =
                                        "var(--bg-hover, rgba(255,255,255,0.04))";
                                }
                            }}
                            onMouseLeave={(e) => {
                                if (s.daemonid !== daemonId) {
                                    (e.currentTarget as HTMLElement).style.background = "transparent";
                                }
                            }}
                        >
                            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                                <span style={{ fontWeight: s.daemonid === daemonId ? 600 : 400 }}>
                                    {s.name || `(${s.connection})`}
                                    {s.daemonid === daemonId && " ✓"}
                                </span>
                                <span
                                    style={{
                                        fontSize: 11,
                                        padding: "1px 6px",
                                        borderRadius: 3,
                                        background:
                                            s.status === "running" ? "rgba(56,178,127,0.2)" : "rgba(100,100,100,0.2)",
                                        color: s.status === "running" ? "#38b27f" : "var(--text-muted)",
                                    }}
                                >
                                    {s.status}
                                </span>
                            </div>
                            <div style={{ fontSize: 11, color: "var(--text-muted)", marginTop: 2 }}>
                                {s.connection} · {s.blocks?.length ?? 0} block(s)
                            </div>
                        </div>
                    ))}
                </div>
            )}
        </div>
    );
}
