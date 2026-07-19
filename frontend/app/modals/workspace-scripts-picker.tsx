// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { getScriptsFromWorkspace, getSortedScripts, runScript } from "@/app/workspace/workspace-scripts";
import { Button } from "@/app/element/button";
import { Input, InputGroup, InputLeftElement } from "@/app/element/input";
import { modalsModel } from "@/app/store/modalmodel";
import { WOS } from "@/app/store/global";
import { formatRelativeTime } from "@/util/util";
import clsx from "clsx";
import { memo, useCallback, useEffect, useRef, useState } from "react";
import { Modal } from "./modal";
import "./workspace-scripts-picker.scss";

interface ScriptPickerProps {
    workspaceId: string;
    blockId?: string;
}

const WorkspaceScriptsPickerComponent = ({ workspaceId, blockId }: ScriptPickerProps) => {
    const [workspace] = WOS.useWaveObjectValue<Workspace>(WOS.makeORef("workspace", workspaceId));
    const [searchText, setSearchText] = useState("");
    const [selectedIndex, setSelectedIndex] = useState(-1);
    const searchInputRef = useRef<HTMLInputElement>(null);
    const listRef = useRef<HTMLDivElement>(null);

    const scripts = getSortedScripts(getScriptsFromWorkspace(workspace));
    const filtered = scripts.filter((s) => {
        if (!searchText) return true;
        const q = searchText.toLowerCase();
        return s.name.toLowerCase().includes(q) || s.command.toLowerCase().includes(q);
    });

    const handleRun = useCallback((script: WorkspaceScript) => {
        const result = runScript(script, blockId);
        if (!result.ok) {
            modalsModel.pushModal("MessageModal", { children: result.reason ?? "Failed to run script." });
            return;
        }
        modalsModel.popModal();
    }, [blockId]);

    const handleManage = useCallback(() => {
        modalsModel.popModal();
        modalsModel.pushModal("WorkspaceScriptsModal", { workspaceId });
    }, [workspaceId]);

    // Reset selection whenever the filter changes
    useEffect(() => {
        setSelectedIndex(-1);
    }, [searchText]);

    // Scroll selected item into view
    useEffect(() => {
        if (selectedIndex < 0 || !listRef.current) return;
        const item = listRef.current.children[selectedIndex] as HTMLElement;
        item?.scrollIntoView({ block: "nearest" });
    }, [selectedIndex]);

    const handleKeyDown = useCallback(
        (e: React.KeyboardEvent) => {
            if (e.key === "ArrowDown") {
                e.preventDefault();
                e.stopPropagation();
                if (filtered.length > 0) {
                    setSelectedIndex((prev) => Math.min(prev + 1, filtered.length - 1));
                }
            } else if (e.key === "ArrowUp") {
                e.preventDefault();
                e.stopPropagation();
                setSelectedIndex((prev) => {
                    if (prev <= 0) {
                        searchInputRef.current?.focus();
                        return -1;
                    }
                    return prev - 1;
                });
            } else if (e.key === "Enter") {
                e.preventDefault();
                e.stopPropagation();
                const idx = selectedIndex >= 0 ? selectedIndex : 0;
                if (idx < filtered.length) {
                    handleRun(filtered[idx]);
                }
            } else if (e.key === "Escape") {
                e.preventDefault();
                e.stopPropagation();
                modalsModel.popModal();
            }
        },
        [filtered, selectedIndex, handleRun]
    );

    if (!workspace) {
        return (
            <Modal className="workspace-scripts-picker" onClose={() => modalsModel.popModal()}>
                <div className="picker-empty">Loading...</div>
            </Modal>
        );
    }

    return (
        <Modal className="workspace-scripts-picker" onClose={() => modalsModel.popModal()}>
            <div className="picker-root" onKeyDown={handleKeyDown}>
                <div className="picker-header">Run Script</div>
                <div className="picker-search">
                    <InputGroup>
                        <InputLeftElement>
                            <i className="fa-sharp fa-solid fa-magnifying-glass"></i>
                        </InputLeftElement>
                        <Input
                            ref={searchInputRef}
                            placeholder="Search scripts..."
                            value={searchText}
                            onChange={setSearchText}
                            autoFocus
                        />
                    </InputGroup>
                </div>
                <div className="picker-list-wrapper">
                    {filtered.length === 0 ? (
                        <div className="picker-empty">
                            {scripts.length === 0 ? (
                                <>
                                    <i className="fa-sharp fa-regular fa-scroll"></i>
                                    <span>No scripts configured</span>
                                    <Button
                                        className="ghost grey"
                                        onClick={() => {
                                            modalsModel.popModal();
                                            modalsModel.pushModal("WorkspaceScriptsModal", { workspaceId });
                                        }}
                                    >
                                        + Add Script
                                    </Button>
                                </>
                            ) : (
                                "No matching scripts"
                            )}
                        </div>
                    ) : (
                        <div className="picker-list" ref={listRef} role="listbox">
                            {filtered.map((script, index) => (
                                <div
                                    key={script.id}
                                    className={clsx("picker-item", { selected: index === selectedIndex })}
                                    role="option"
                                    aria-selected={index === selectedIndex}
                                    onMouseEnter={() => setSelectedIndex(index)}
                                    onClick={() => handleRun(script)}
                                >
                                    <div className="picker-item-name">{script.name}</div>
                                    {script.desc && <div className="picker-item-desc">{script.desc}</div>}
                                    {script.lastused != null && (
                                        <div className="picker-item-time">{formatRelativeTime(script.lastused)}</div>
                                    )}
                                </div>
                            ))}
                        </div>
                    )}
                </div>
                <div className="picker-footer">
                    <Button className="ghost grey" onClick={handleManage}>
                        Manage Scripts...
                    </Button>
                </div>
            </div>
        </Modal>
    );
};

export const WorkspaceScriptsPicker = memo(WorkspaceScriptsPickerComponent);
WorkspaceScriptsPicker.displayName = "WorkspaceScriptsPicker";
