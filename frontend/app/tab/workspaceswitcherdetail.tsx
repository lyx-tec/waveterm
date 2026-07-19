// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { useWaveEnv, WaveEnv, WaveEnvSubset } from "@/app/waveenv/waveenv";
import { fireAndForget, makeIconClass } from "@/util/util";
import clsx from "clsx";
import { OverlayScrollbarsComponent } from "overlayscrollbars-react";
import { useCallback, useEffect, useState } from "react";
import { IconButton } from "../element/iconbutton";
import { WorkspaceEditor } from "./workspaceeditor";

type WorkspaceSwitcherDetailEnv = WaveEnvSubset<{
    services: {
        workspace: WaveEnv["services"]["workspace"];
    };
}>;

type WorkspaceListEntry = {
    windowId: string;
    workspace: Workspace;
};

type WorkspaceDraft = {
    name: string;
    icon: string;
    color: string;
    defaultconnname: string;
    defaultcwd: string;
};

function makeWorkspaceDraft(workspace: Workspace): WorkspaceDraft {
    return {
        name: workspace.name,
        icon: workspace.icon,
        color: workspace.color,
        defaultconnname: workspace.defaultconnname ?? "",
        defaultcwd: workspace.defaultcwd ?? "",
    };
}

function isWorkspaceDraftChanged(workspace: Workspace, draft: WorkspaceDraft): boolean {
    return (
        workspace.name !== draft.name ||
        workspace.icon !== draft.icon ||
        workspace.color !== draft.color ||
        (workspace.defaultconnname ?? "") !== draft.defaultconnname ||
        (workspace.defaultcwd ?? "") !== draft.defaultcwd
    );
}

type WorkspaceSwitcherDetailProps = {
    entry: WorkspaceListEntry;
    onBack: () => void;
    onDeleteWorkspace: (workspaceId: string) => void;
    onWorkspaceUpdated: (workspace: Workspace) => void;
};

const WorkspaceSwitcherDetail = ({
    entry,
    onBack,
    onDeleteWorkspace,
    onWorkspaceUpdated,
}: WorkspaceSwitcherDetailProps) => {
    const env = useWaveEnv<WorkspaceSwitcherDetailEnv>();
    const workspace = entry.workspace;
    const [draft, setDraft] = useState<WorkspaceDraft>(() => makeWorkspaceDraft(workspace));
    const [saving, setSaving] = useState(false);

    useEffect(() => {
        setDraft(makeWorkspaceDraft(workspace));
    }, [workspace.oid]);

    const hasChanges = isWorkspaceDraftChanged(workspace, draft);
    const canSave = hasChanges && draft.name !== "" && !saving;

    const setWorkspaceField = useCallback((patch: Partial<WorkspaceDraft>) => {
        setDraft((currentDraft) => ({ ...currentDraft, ...patch }));
    }, []);

    const saveWorkspace = useCallback(() => {
        if (!canSave) {
            return;
        }
        fireAndForget(async () => {
            setSaving(true);
            try {
                await env.services.workspace.UpdateWorkspace(
                    workspace.oid,
                    draft.name,
                    draft.icon,
                    draft.color,
                    draft.defaultconnname,
                    draft.defaultcwd,
                    false
                );
                onWorkspaceUpdated({
                    ...workspace,
                    name: draft.name,
                    icon: draft.icon,
                    color: draft.color,
                    defaultconnname: draft.defaultconnname,
                    defaultcwd: draft.defaultcwd,
                });
            } finally {
                setSaving(false);
            }
        });
    }, [canSave, draft, workspace]);

    const backIconDecl: IconButtonDecl = {
        elemtype: "iconbutton",
        className: "back",
        icon: "chevron-left",
        title: "Back to workspaces",
        click: onBack,
    };
    const saveIconDecl: IconButtonDecl = {
        elemtype: "iconbutton",
        className: clsx("save", { changed: canSave }),
        icon: saving ? "refresh" : "floppy-disk",
        iconSpin: saving,
        title: hasChanges ? "Save workspace" : "No workspace changes",
        disabled: !canSave,
        click: () => saveWorkspace(),
    };

    return (
        <div className="workspace-detail-page">
            <div className="detail-header">
                <IconButton decl={backIconDecl} />
                <i
                    className={clsx("detail-workspace-icon", makeIconClass(draft.icon, true))}
                    style={{ color: draft.color }}
                />
                <div className="detail-title">{draft.name}</div>
                <IconButton decl={saveIconDecl} />
            </div>
            <OverlayScrollbarsComponent className="detail-scrollable" options={{ scrollbars: { autoHide: "leave" } }}>
                <WorkspaceEditor
                    title={draft.name}
                    icon={draft.icon}
                    color={draft.color}
                    connName={draft.defaultconnname}
                    cwd={draft.defaultcwd}
                    focusInput
                    workspaceId={workspace.oid}
                    onTitleChange={(newTitle) => setWorkspaceField({ name: newTitle })}
                    onColorChange={(newColor) => setWorkspaceField({ color: newColor })}
                    onIconChange={(newIcon) => setWorkspaceField({ icon: newIcon })}
                    onConnNameChange={(newConnName) => setWorkspaceField({ defaultconnname: newConnName })}
                    onCwdChange={(newCwd) => setWorkspaceField({ defaultcwd: newCwd })}
                    onDeleteWorkspace={() => onDeleteWorkspace(workspace.oid)}
                />
            </OverlayScrollbarsComponent>
        </div>
    );
};

export { WorkspaceSwitcherDetail };
