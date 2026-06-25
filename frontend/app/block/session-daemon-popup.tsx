// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { FloatingPortal } from "@floating-ui/react";
import { SessionDaemonIndicatorState } from "./session-daemon-hooks";
import { SessionCreateRow, SessionRow } from "./session-daemon-rows";

const SessionDescription =
    "Shared Session introduces persistent, reusable SSH terminal sessions that are no longer tied to a single block. Users can name sessions, attach multiple blocks on the same connection, switch between active sessions from the block header, and keep remote work available across block changes, reconnects, and idle periods with automatic cleanup.";

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

interface SessionDaemonPopupProps {
    state: SessionDaemonIndicatorState;
}

export function SessionDaemonPopup({ state }: SessionDaemonPopupProps) {
    if (!state.showPopup) {
        return null;
    }
    return (
        <FloatingPortal>
            <div
                ref={state.popupRef}
                style={{ ...popupStyle, ...state.floatingStyles }}
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
                        <span
                            className="group"
                            style={{
                                position: "relative",
                                display: "inline-flex",
                                alignItems: "center",
                                color: "var(--text-muted)",
                                cursor: "default",
                                fontSize: 12,
                            }}
                            aria-label="Shared session description"
                        >
                            <i className="fa-sharp fa-solid fa-circle-question" />
                            <span
                                className="hidden group-hover:block"
                                style={{
                                    position: "absolute",
                                    top: 18,
                                    left: -52,
                                    width: "min(300px, calc(100vw - 56px))",
                                    padding: "8px 10px",
                                    borderRadius: 8,
                                    border: "1px solid rgba(148, 163, 184, 0.18)",
                                    background: "color-mix(in srgb, var(--bg-secondary, #1e1e2e) 98%, black)",
                                    boxShadow: "0 12px 28px rgba(0,0,0,0.36)",
                                    color: "var(--text-primary)",
                                    fontSize: 11,
                                    lineHeight: 1.45,
                                    fontWeight: 400,
                                    zIndex: 1,
                                }}
                            >
                                {SessionDescription}
                            </span>
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
                        {state.sameConnSessions.length}
                    </span>
                </div>
                <SessionCreateRow state={state} />
                {state.sameConnSessions.length === 0 && (
                    <div style={{ fontSize: 12, color: "var(--text-muted)", padding: "14px 10px" }}>
                        No sessions on this connection
                    </div>
                )}
                {state.sameConnSessions.map((session) => (
                    <SessionRow key={session.daemonid} session={session} state={state} />
                ))}
            </div>
        </FloatingPortal>
    );
}
