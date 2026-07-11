// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

function pad(n: number, len = 2): string {
    return String(n).padStart(len, "0");
}

function formatTimestamp(d: Date = new Date()): string {
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`;
}

function safeStr(a: any): string {
    if (typeof a === "string") return a;
    if (a instanceof Error) return a.stack || a.message;
    try {
        return JSON.stringify(a);
    } catch {
        return String(a);
    }
}

function termLog(...args: any[]): void {
    const line = `${formatTimestamp()} [term] ${args.map(safeStr).join(" ")}`;
    try {
        const api = (window as any).api;
        api?.sendLog?.(line);
    } catch (_) {
        // sendLog not available (preview/mock) — fall through
    }
    console.log(line);
}

export { termLog, formatTimestamp };
