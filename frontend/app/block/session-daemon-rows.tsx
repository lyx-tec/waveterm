// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { SessionDaemonIndicatorState } from "./session-daemon-hooks";
import { SessionInfo } from "./session-daemon-types";

const truncateStyle = {
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
} as const;

function formatCreatedTime(ms: number | undefined): string {
    if (ms == null) return "";
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

interface SessionCreateRowProps {
    state: SessionDaemonIndicatorState;
}

export function SessionCreateRow({ state }: SessionCreateRowProps) {
    if (state.showCreateInput) {
        return (
            <div
                style={{
                    display: "flex",
                    alignItems: "center",
                    padding: "4px 8px",
                    marginBottom: 4,
                    borderRadius: 8,
                    background: "rgba(56, 189, 248, 0.08)",
                    border: "1px solid rgba(56, 189, 248, 0.30)",
                    opacity: state.creating ? 0.5 : 1,
                }}
            >
                <i className="fa-sharp fa-solid fa-plus" style={{ color: "#38bdf8", fontSize: 13, marginRight: 8 }} />
                <input
                    ref={state.createInputRef}
                    type="text"
                    value={state.newSessionName}
                    disabled={state.creating}
                    onChange={(e) => state.setNewSessionName(e.target.value)}
                    onKeyDown={(e) => {
                        if (e.key === "Enter") {
                            const name = state.newSessionName.trim();
                            state.handleCreateAndAttach(name || undefined);
                            state.setShowCreateInput(false);
                            state.setNewSessionName("");
                        }
                        if (e.key === "Escape") {
                            state.setShowCreateInput(false);
                            state.setNewSessionName("");
                        }
                    }}
                    placeholder="Session name (optional)"
                    style={{
                        flex: 1,
                        background: "transparent",
                        border: "none",
                        outline: "none",
                        color: "#7dd3fc",
                        fontSize: 13,
                        fontWeight: 600,
                    }}
                />
            </div>
        );
    }
    return (
        <div
            onClick={() => {
                state.setShowCreateInput(true);
                setTimeout(() => state.createInputRef.current?.focus(), 0);
            }}
            style={{
                display: "flex",
                alignItems: "center",
                gap: 8,
                padding: "8px 10px",
                marginBottom: 4,
                cursor: state.creating ? "default" : "pointer",
                borderRadius: 8,
                background: "rgba(56, 189, 248, 0.08)",
                border: "1px solid rgba(56, 189, 248, 0.18)",
                opacity: state.creating ? 0.5 : 1,
            }}
        >
            <i className={`fa-sharp fa-solid ${state.creating ? "fa-spinner fa-spin" : "fa-plus"}`} style={{ color: "#38bdf8", fontSize: 13 }} />
            <span style={{ fontSize: 13, fontWeight: 600, color: "#7dd3fc" }}>
                {state.creating ? "Creating..." : "Create new session"}
            </span>
        </div>
    );
}

interface SessionRowProps {
    session: SessionInfo;
    state: SessionDaemonIndicatorState;
}

export function SessionRow({ session, state }: SessionRowProps) {
    const isActive = session.daemonid === state.daemonId;
    const blockCount = session.blocks?.length ?? 0;
    const canClose = blockCount === 0 || session.status === "done";
    const displayStatus = session.status === "done" ? "done" : blockCount === 0 ? "idle" : session.status;
    return (
        <div
            onClick={() => state.handleAttach(session.daemonid)}
            title={`${session.name || session.connection} · ${session.status}`}
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
                border: isActive ? "1px solid rgba(56, 189, 248, 0.24)" : "1px solid transparent",
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
                        background: isActive ? "rgba(56, 189, 248, 0.16)" : "rgba(148, 163, 184, 0.08)",
                    }}
                >
                    <i className={`fa-sharp fa-solid ${session.isanonymous ? "fa-link" : "fa-tag"}`} />
                </span>
                <div style={{ minWidth: 0 }}>
                    {state.editingId === session.daemonid ? (
                        <input
                            ref={state.editInputRef}
                            type="text"
                            value={state.editName}
                            onChange={(e) => state.setEditName(e.target.value)}
                            onKeyDown={(e) => {
                                if (e.key === "Enter") state.handleSaveEdit();
                                if (e.key === "Escape") state.handleCancelEdit();
                            }}
                            onBlur={state.handleSaveEdit}
                            onClick={(e) => e.stopPropagation()}
                            style={{
                                width: "100%",
                                fontWeight: 650,
                                color: "var(--text-primary)",
                                fontSize: 14,
                                lineHeight: "20px",
                                background: "rgba(148, 163, 184, 0.12)",
                                border: "1px solid rgba(56, 189, 248, 0.3)",
                                borderRadius: 4,
                                padding: "1px 6px",
                                outline: "none",
                            }}
                        />
                    ) : (
                        <div
                            onClick={(e) => {
                                e.stopPropagation();
                                state.handleStartEdit(session.daemonid, session.name);
                            }}
                            style={{ display: "flex", alignItems: "center", gap: 6, cursor: "text" }}
                            title="Click to rename"
                        >
                            <span style={{ ...truncateStyle, fontWeight: 650, color: "var(--text-primary)", fontSize: 14 }}>
                                {session.name || "Unnamed session"}
                            </span>
                            <i
                                className="fa-sharp fa-solid fa-pencil"
                                style={{ fontSize: 9, color: "var(--text-muted)", opacity: 0.45, flexShrink: 0 }}
                            />
                        </div>
                    )}
                    {session.connection && (
                        <div style={{ ...truncateStyle, fontSize: 11, color: "var(--text-muted)", marginTop: 1, fontFamily: "monospace" }}>
                            {session.connection}
                        </div>
                    )}
                    <div style={{ ...truncateStyle, fontSize: 11, color: "var(--text-muted)", marginTop: 1, fontFamily: "monospace" }}>
                        Sess: {session.daemonid.slice(0, 8)}
                    </div>
                    {session.jobid && (
                        <div style={{ ...truncateStyle, fontSize: 10, color: "var(--text-muted)", opacity: 0.6, fontFamily: "monospace" }}>
                            Job: {session.jobid.slice(0, 8)}
                        </div>
                    )}
                </div>
            </div>
            <div style={{ display: "flex", flexDirection: "column", alignItems: "flex-end", gap: 5 }}>
                <SessionStatusPill status={displayStatus} />
                <span style={{ fontSize: 10, color: "var(--text-muted)", opacity: 0.6 }}>
                    {formatCreatedTime(session.createdat)}
                </span>
                {canClose ? (
                    <span
                        onClick={(e) => {
                            e.stopPropagation();
                            state.handleDelete(session.daemonid);
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
                        title="Close session"
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
}
