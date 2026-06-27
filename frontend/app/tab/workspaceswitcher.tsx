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
import { CSSProperties, forwardRef, useCallback, useEffect, useMemo } from "react";
import WorkspaceSVG from "../asset/workspace.svg";
import { IconButton } from "../element/iconbutton";
import { makeORef } from "../store/wos";
import { waveEventSubscribeSingle } from "../store/wps";
import "./workspaceswitcher.scss";
import { WorkspaceSwitcherDetail } from "./workspaceswitcherdetail";

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

type WorkspaceList = WorkspaceListEntry[];
const workspaceMapAtom = atom<WorkspaceList>([]);
const workspaceSplitAtom = splitAtom(workspaceMapAtom);
const editingWorkspaceAtom = atom<string>();

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

    const onWorkspaceUpdated = useCallback((updatedWorkspace: Workspace) => {
        setWorkspaceList((workspaceEntries) =>
            workspaceEntries.map((workspaceEntry) =>
                workspaceEntry.workspace.oid === updatedWorkspace.oid
                    ? { ...workspaceEntry, workspace: updatedWorkspace }
                    : workspaceEntry
            )
        );
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
                    <WorkspaceSwitcherDetail
                        entry={editingWorkspaceEntry}
                        onBack={() => setEditingWorkspace(null)}
                        onDeleteWorkspace={onDeleteWorkspace}
                        onWorkspaceUpdated={onWorkspaceUpdated}
                    />
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

export { WorkspaceSwitcher };
