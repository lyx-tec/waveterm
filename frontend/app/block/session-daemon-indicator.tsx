// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { useSessionDaemonIndicator } from "./session-daemon-hooks";
import { SessionDaemonPopup } from "./session-daemon-popup";

interface SessionDaemonIndicatorProps {
    blockId: string;
    useTermHeader: boolean;
}

export function SessionDaemonIndicator({ blockId, useTermHeader }: SessionDaemonIndicatorProps) {
    const state = useSessionDaemonIndicator(blockId);

    if (!useTermHeader) {
        return null;
    }

    return (
        <>
            <div
                ref={state.iconRef}
                className="iconbutton text-[13px] ml-[-4px]"
                title={state.daemonId ? `Session: ${state.daemonId}` : "No session attached"}
                onClick={() => state.setShowPopup((v) => !v)}
                style={{ display: state.visible ? "inline-flex" : "none", alignItems: "center", gap: 4 }}
            >
                <i className={`fa-sharp fa-solid ${state.daemonId ? "fa-link text-sky-500" : "fa-link-slash text-muted"}`} />
                {state.daemonId ? (
                    <span style={{ fontSize: 11, color: "var(--text-muted)", maxWidth: 72, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                        {state.sessionDisplay.isanonymous ? state.daemonId.slice(0, 8) : (state.sessionDisplay.name || state.daemonId.slice(0, 8))}
                    </span>
                ) : (
                    <span style={{ fontSize: 11, color: "var(--text-muted)", opacity: 0.6 }}>
                        non session
                    </span>
                )}
            </div>
            <SessionDaemonPopup state={state} />
        </>
    );
}
