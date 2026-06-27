// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { globalStore } from "@/app/store/jotaiStore";
import { useWaveEnv, WaveEnv, WaveEnvSubset } from "@/app/waveenv/waveenv";
import {
    ExpandableMenu,
    ExpandableMenuItem,
    ExpandableMenuItemLeftElement,
    ExpandableMenuItemRightElement,
} from "@/element/expandablemenu";
import { Popover, PopoverButton, PopoverContent } from "@/element/popover";
import { fireAndForget, makeIconClass, useAtomValueSafe } from "@/util/util";
import clsx from "clsx";
import { atom, PrimitiveAtom, useAtomValue, useSetAtom } from "jotai";
import { splitAtom } from "jotai/utils";
import { OverlayScrollbarsComponent } from "overlayscrollbars-react";
import { CSSProperties, forwardRef, useCallback, useEffect, useMemo, useState } from "react";
import WorkspaceSVG from "../asset/workspace.svg";
import { IconButton } from "../element/iconbutton";
import { makeORef } from "../store/wos";
import { waveEventSubscribeSingle } from "../store/wps";
import { WorkspaceEditor } from "./workspaceeditor";
import "./workspaceswitcher.scss";

export type WorkspaceSwitcherEnv = WaveEnvSubset<{
    electron: {
        deleteWorkspace: WaveEnv["electron"]["deleteWorkspace"];
        createWorkspace: WaveEnv["electron"]["createWorkspace"];
        switchWorkspace: WaveEnv["electron"]["switchWorkspace"];
    };
    atoms: {
        workspace: WaveEnv["atoms"]["workspace"];
    };
    services: {
        workspace: WaveEnv["services"]["workspace"];
    };
    wos: WaveEnv["wos"];
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

type WorkspaceList = WorkspaceListEntry[];
const workspaceMapAtom = atom<WorkspaceList>([]);
const workspaceSplitAtom = splitAtom(workspaceMapAtom);
const editingWorkspaceAtom = atom<string>();

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

const WorkspaceSwitcher = forwardRef<HTMLDivElement>((_, ref) => {
    const env = useWaveEnv<WorkspaceSwitcherEnv>();
    const setWorkspaceList = useSetAtom(workspaceMapAtom);
    const activeWorkspace = useAtomValueSafe(env.atoms.workspace);
    const workspaceEntries = useAtomValue(workspaceMapAtom);
    const workspaceList = useAtomValue(workspaceSplitAtom);
    const editingWorkspace = useAtomValue(editingWorkspaceAtom);
    const setEditingWorkspace = useSetAtom(editingWorkspaceAtom);

    const updateWorkspaceList = useCallback(async () => {
        const workspaceList = await env.services.workspace.ListWorkspaces();
        if (!workspaceList) {
            return;
        }
        const newList: WorkspaceList = [];
        for (const entry of workspaceList) {
            // This just ensures that the atom exists for easier setting of the object
            globalStore.get(env.wos.getWaveObjectAtom(makeORef("workspace", entry.workspaceid)));
            newList.push({
                windowId: entry.windowid,
                workspace: await env.services.workspace.GetWorkspace(entry.workspaceid),
            });
        }
        setWorkspaceList(newList);
    }, []);

    useEffect(
        () =>
            waveEventSubscribeSingle({
                eventType: "workspace:update",
                handler: () => fireAndForget(updateWorkspaceList),
            }),
        []
    );

    useEffect(() => {
        fireAndForget(updateWorkspaceList);
    }, []);

    const onDeleteWorkspace = useCallback((workspaceId: string) => {
        env.electron.deleteWorkspace(workspaceId);
    }, []);

    const isActiveWorkspaceSaved = !!(activeWorkspace.name && activeWorkspace.icon);
    const editingWorkspaceEntry = useMemo(
        () => workspaceEntries.find((entry) => entry.workspace.oid === editingWorkspace),
        [editingWorkspace, workspaceEntries]
    );

    const workspaceIcon = isActiveWorkspaceSaved ? (
        <i className={makeIconClass(activeWorkspace.icon, false)} style={{ color: activeWorkspace.color }}></i>
    ) : (
        <WorkspaceSVG />
    );

    const saveWorkspace = () => {
        fireAndForget(async () => {
            await env.services.workspace.UpdateWorkspace(
                activeWorkspace.oid,
                "",
                "",
                "",
                activeWorkspace.defaultconnname ?? "",
                activeWorkspace.defaultcwd ?? "",
                true
            );
            await updateWorkspaceList();
            setEditingWorkspace(activeWorkspace.oid);
        });
    };

    return (
        <Popover
            className="workspace-switcher-popover"
            placement="bottom-start"
            onDismiss={() => setEditingWorkspace(null)}
            ref={ref}
        >
            <PopoverButton
                className="workspace-switcher-button grey"
                as="div"
                onClick={() => {
                    fireAndForget(updateWorkspaceList);
                }}
            >
                <span className="workspace-icon">{workspaceIcon}</span>
            </PopoverButton>
            <PopoverContent className="workspace-switcher-content">
                {editingWorkspaceEntry ? (
                    <WorkspaceSwitcherDetail entry={editingWorkspaceEntry} onDeleteWorkspace={onDeleteWorkspace} />
                ) : (
                    <>
                        <div className="title">{isActiveWorkspaceSaved ? "Switch workspace" : "Open workspace"}</div>
                        <OverlayScrollbarsComponent
                            className={"scrollable"}
                            options={{ scrollbars: { autoHide: "leave" } }}
                        >
                            <ExpandableMenu noIndent singleOpen>
                                {workspaceList.map((entry, i) => (
                                    <WorkspaceSwitcherItem key={i} entryAtom={entry} />
                                ))}
                            </ExpandableMenu>
                        </OverlayScrollbarsComponent>

                        <div className="actions">
                            {isActiveWorkspaceSaved ? (
                                <ExpandableMenuItem onClick={() => env.electron.createWorkspace()}>
                                    <ExpandableMenuItemLeftElement>
                                        <i className="fa-sharp fa-solid fa-plus"></i>
                                    </ExpandableMenuItemLeftElement>
                                    <div className="content">Create new workspace</div>
                                </ExpandableMenuItem>
                            ) : (
                                <ExpandableMenuItem onClick={() => saveWorkspace()}>
                                    <ExpandableMenuItemLeftElement>
                                        <i className="fa-sharp fa-solid fa-floppy-disk"></i>
                                    </ExpandableMenuItemLeftElement>
                                    <div className="content">Save workspace</div>
                                </ExpandableMenuItem>
                            )}
                        </div>
                    </>
                )}
            </PopoverContent>
        </Popover>
    );
});

const WorkspaceSwitcherItem = ({ entryAtom }: { entryAtom: PrimitiveAtom<WorkspaceListEntry> }) => {
    const env = useWaveEnv<WorkspaceSwitcherEnv>();
    const activeWorkspace = useAtomValueSafe(env.atoms.workspace);
    const workspaceEntry = useAtomValue(entryAtom);
    const setEditingWorkspace = useSetAtom(editingWorkspaceAtom);

    const workspace = workspaceEntry.workspace;
    const isCurrentWorkspace = activeWorkspace.oid === workspace.oid;

    const isActive = !!workspaceEntry.windowId;
    const editIconDecl: IconButtonDecl = {
        elemtype: "iconbutton",
        className: "edit",
        icon: "pencil",
        title: "Edit workspace",
        click: (e) => {
            e.stopPropagation();
            setEditingWorkspace(workspace.oid);
        },
    };
    const windowIconDecl: IconButtonDecl = {
        elemtype: "iconbutton",
        className: "window",
        noAction: true,
        icon: "window",
        title: "This workspace is open in another window",
    };

    return (
        <div key={workspace.oid} className={clsx({ "is-current": isCurrentWorkspace })}>
            <ExpandableMenuItem
                className="workspace-list-item"
                onClick={() => {
                    env.electron.switchWorkspace(workspace.oid);
                    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
                }}
            >
                <div
                    className="menu-group-title-wrapper"
                    style={
                        {
                            "--workspace-color": workspace.color,
                        } as CSSProperties
                    }
                >
                    <ExpandableMenuItemLeftElement>
                        <i
                            className={clsx("left-icon", makeIconClass(workspace.icon, true))}
                            style={{ color: workspace.color }}
                        />
                    </ExpandableMenuItemLeftElement>
                    <div className="workspace-item-text">
                        <div className="label">{workspace.name}</div>
                        {(workspace.defaultconnname || workspace.defaultcwd) && (
                            <div className="meta">{workspace.defaultconnname || workspace.defaultcwd}</div>
                        )}
                    </div>
                    <ExpandableMenuItemRightElement>
                        <div className="icons">
                            <IconButton decl={editIconDecl} />
                            {isActive && !isCurrentWorkspace && <IconButton decl={windowIconDecl} />}
                        </div>
                    </ExpandableMenuItemRightElement>
                </div>
            </ExpandableMenuItem>
        </div>
    );
};

const WorkspaceSwitcherDetail = ({
    entry,
    onDeleteWorkspace,
}: {
    entry: WorkspaceListEntry;
    onDeleteWorkspace: (workspaceId: string) => void;
}) => {
    const env = useWaveEnv<WorkspaceSwitcherEnv>();
    const setWorkspaceList = useSetAtom(workspaceMapAtom);
    const setEditingWorkspace = useSetAtom(editingWorkspaceAtom);
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
                const updated = {
                    ...workspace,
                    name: draft.name,
                    icon: draft.icon,
                    color: draft.color,
                    defaultconnname: draft.defaultconnname,
                    defaultcwd: draft.defaultcwd,
                };
                setWorkspaceList((workspaceEntries) =>
                    workspaceEntries.map((workspaceEntry) =>
                        workspaceEntry.workspace.oid === workspace.oid
                            ? { ...workspaceEntry, workspace: updated }
                            : workspaceEntry
                    )
                );
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
        click: () => setEditingWorkspace(null),
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

export { WorkspaceSwitcher };
