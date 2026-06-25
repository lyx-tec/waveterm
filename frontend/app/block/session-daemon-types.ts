// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

export interface SessionDisplayData {
    name: string | null;
    isanonymous: boolean;
}

export interface SessionInfo {
    daemonid: string;
    name: string;
    connection: string;
    status: string;
    isanonymous: boolean;
    createdat?: number;
    blocks?: string[];
    jobid?: string;
    lastactiveat?: number;
}
