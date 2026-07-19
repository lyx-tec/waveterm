// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { atoms, getBlockMetaKeyAtom, getFocusedBlockId, getOverrideConfigAtom, globalStore, WOS } from "@/app/store/global";
import { modalsModel } from "@/app/store/modalmodel";
import { ObjectService } from "@/app/store/services";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { isWindows } from "@/util/platformutil";
import { fireAndForget, isLocalConnName, stringToBase64 } from "@/util/util";

const TempFileVar = "__WAVE_SCR_FILE";

const MetaKeyScriptsList = "scripts:list";

/** Open the script picker modal, if the focused block is a terminal.
 *  Returns true if the modal was opened, false if the condition was not met
 *  (caller should not swallow the key event). */
export function openScriptsPicker(blockIdOverride?: string): boolean {
    const blockId = blockIdOverride ?? getFocusedBlockId();
    if (blockId == null) return false;
    const view = globalStore.get(getBlockMetaKeyAtom(blockId, "view"));
    if (view !== "term") return false;
    const workspaceId = globalStore.get(atoms.workspaceId);
    if (!workspaceId) return false;
    if (modalsModel.isModalOpen("WorkspaceScriptsPicker")) return false;
    modalsModel.pushModal("WorkspaceScriptsPicker", { workspaceId, blockId });
    return true;
}

export function getScriptsFromWorkspace(workspace: Workspace): WorkspaceScript[] {
    const raw = workspace?.meta?.[MetaKeyScriptsList];
    if (!Array.isArray(raw)) {
        return [];
    }
    return raw.filter(
        (s: any) => s != null && typeof s.id === "string" && typeof s.name === "string" && typeof s.command === "string"
    );
}

export function getSortedScripts(scripts: WorkspaceScript[]): WorkspaceScript[] {
    return [...scripts].sort((a, b) => (b.lastused ?? 0) - (a.lastused ?? 0));
}

// Builds the shell text injected into the terminal to run a script.
// The script body is written to a unique temp file (mktemp) via heredoc, then
// sourced (using POSIX `.`) in the current shell (so cd/export persist) and
// always cleaned up.
function buildHeredocCommand(script: WorkspaceScript): string {
    const delimiter = `WST_SCRIPT_END_${crypto.randomUUID().replace(/-/g, "").slice(0, 12)}`;
    const lines = [
        `${TempFileVar}=$(mktemp "\${TMPDIR:-/tmp}/waveterm-script-XXXXXX"); cat << '${delimiter}' > "$${TempFileVar}"`,
        script.command,
        delimiter,
        `. "$${TempFileVar}"; rm -f "$${TempFileVar}"`,
    ];
    return lines.join("\n") + "\n";
}

export function saveScriptsToWorkspace(workspaceId: string, scripts: WorkspaceScript[]): void {
    const oref = WOS.makeORef("workspace", workspaceId);
    fireAndForget(() => ObjectService.UpdateObjectMeta(oref, { [MetaKeyScriptsList]: scripts }));
}

// Read-modify-write of the scripts array; concurrent edits could theoretically
// clobber each other, but scripts are edited infrequently by a single user.
export function recordScriptRun(workspaceId: string, scriptId: string): void {
    const oref = WOS.makeORef("workspace", workspaceId);
    const workspace = WOS.getObjectValue<Workspace>(oref);
    if (!workspace) return;
    const scripts = getScriptsFromWorkspace(workspace);
    const updated = scripts.map((s) => (s.id === scriptId ? { ...s, lastused: Date.now() } : s));
    fireAndForget(() => ObjectService.UpdateObjectMeta(oref, { [MetaKeyScriptsList]: updated }));
}

// The heredoc/`.` runner only works on POSIX shells (bash/zsh/sh/dash). Non-POSIX
// shells (fish, Windows cmd/PowerShell) would produce syntax errors, so we detect
// and block them. WSL/SSH remote connections are assumed POSIX-compatible.
export function isScriptRunSupportedForBlock(blockId: string): boolean {
    const connName = globalStore.get(getBlockMetaKeyAtom(blockId, "connection"));
    if (!isLocalConnName(connName)) {
        return true; // WSL/SSH remote shells are POSIX
    }
    const shellPath = globalStore.get(getOverrideConfigAtom(blockId, "term:localshellpath")) ?? "";
    if (isWindows()) {
        return /bash/i.test(shellPath);
    }
    // macOS/Linux: fish is the only common non-POSIX shell
    return !/fish/i.test(shellPath);
}

export interface ScriptRunResult {
    ok: boolean;
    reason?: string;
}

export function runScript(script: WorkspaceScript, blockIdOverride?: string): ScriptRunResult {
    const blockId = blockIdOverride ?? getFocusedBlockId();
    if (blockId == null) {
        return { ok: false };
    }
    const view = globalStore.get(getBlockMetaKeyAtom(blockId, "view"));
    if (view !== "term") {
        return { ok: false };
    }
    if (!isScriptRunSupportedForBlock(blockId)) {
        return {
            ok: false,
            reason: "Workspace scripts require a POSIX shell (bash/zsh/sh). Switch to bash, zsh, or sh to run scripts.",
        };
    }
    const cmd = buildHeredocCommand(script);
    RpcApi.ControllerInputCommand(TabRpcClient, { blockid: blockId, inputdata64: stringToBase64(cmd) });

    const workspaceId = globalStore.get(atoms.workspaceId);
    if (workspaceId) {
        recordScriptRun(workspaceId, script.id);
    }
    return { ok: true };
}
