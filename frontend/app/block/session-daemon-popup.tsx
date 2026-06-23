// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { FloatingPortal } from "@floating-ui/react";
import { SessionDaemonIndicatorState } from "./session-daemon-hooks";
import { SessionCreateRow, SessionRow } from "./session-daemon-rows";

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
                        {state.sessions.length}
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
