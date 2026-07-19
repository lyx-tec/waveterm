// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { Button } from "@/app/element/button";
import { Input } from "@/app/element/input";
import { modalsModel } from "@/app/store/modalmodel";
import { WOS } from "@/app/store/global";
import {
    getScriptsFromWorkspace,
    saveScriptsToWorkspace,
} from "@/app/workspace/workspace-scripts";
import { Modal } from "./modal";
import "./workspace-scripts-modal.scss";
import { memo, useCallback, useState } from "react";

interface WorkspaceScriptsModalProps {
    workspaceId: string;
}

const WorkspaceScriptsModalComponent = ({ workspaceId }: WorkspaceScriptsModalProps) => {
    const [workspace] = WOS.useWaveObjectValue<Workspace>(WOS.makeORef("workspace", workspaceId));
    const [editingId, setEditingId] = useState<string | null>(null);
    const [showAddForm, setShowAddForm] = useState(false);
    const [draftName, setDraftName] = useState("");
    const [draftCommand, setDraftCommand] = useState("");
    const [draftDesc, setDraftDesc] = useState("");
    const [errorMsg, setErrorMsg] = useState("");

    const scripts = getScriptsFromWorkspace(workspace);
    const isEditing = editingId != null || showAddForm;

    const resetForm = useCallback(() => {
        setDraftName("");
        setDraftCommand("");
        setDraftDesc("");
        setEditingId(null);
        setShowAddForm(false);
        setErrorMsg("");
    }, []);

    const startEdit = useCallback((script: WorkspaceScript) => {
        setEditingId(script.id);
        setDraftName(script.name);
        setDraftCommand(script.command);
        setDraftDesc(script.desc ?? "");
        setErrorMsg("");
    }, []);

    const startAdd = useCallback(() => {
        resetForm();
        setShowAddForm(true);
    }, [resetForm]);

    const validateForm = useCallback((): string | null => {
        const name = draftName.trim();
        if (!name) return "Script name is required";
        const command = draftCommand.trim();
        if (!command) return "Command is required";
        const isDup = scripts.some((s) => s.name === name && s.id !== editingId);
        if (isDup) return `A script named "${name}" already exists`;
        return null;
    }, [draftName, draftCommand, scripts, editingId]);

    const handleSave = useCallback(() => {
        const err = validateForm();
        if (err) {
            setErrorMsg(err);
            return;
        }
        setErrorMsg("");
        if (editingId) {
            const updated = scripts.map((s) =>
                s.id === editingId
                    ? { ...s, name: draftName.trim(), command: draftCommand.trim(), desc: draftDesc.trim() || undefined }
                    : s
            );
            saveScriptsToWorkspace(workspaceId, updated);
        } else {
            const newScript: WorkspaceScript = {
                id: crypto.randomUUID(),
                name: draftName.trim(),
                command: draftCommand.trim(),
                desc: draftDesc.trim() || undefined,
            };
            saveScriptsToWorkspace(workspaceId, [...scripts, newScript]);
        }
        resetForm();
    }, [editingId, draftName, draftCommand, draftDesc, scripts, workspaceId, validateForm, resetForm]);

    const handleDelete = useCallback(
        (script: WorkspaceScript) => {
            modalsModel.pushModal("ConfirmModal", {
                message: `Delete script "${script.name}"?\nThis action cannot be undone.`,
                onOk: () => {
                    const updated = scripts.filter((s) => s.id !== script.id);
                    saveScriptsToWorkspace(workspaceId, updated);
                },
            });
        },
        [scripts, workspaceId]
    );

    // Handle Escape locally so the global modal-pop handler doesn't also fire.
    // While editing, Escape only cancels the form; otherwise it closes the modal.
    const handleKeyDown = useCallback(
        (e: React.KeyboardEvent) => {
            if (e.key !== "Escape") return;
            e.preventDefault();
            e.stopPropagation();
            if (isEditing) {
                resetForm();
            } else {
                modalsModel.popModal();
            }
        },
        [isEditing, resetForm]
    );

    if (!workspace) {
        return (
            <Modal className="workspace-scripts-modal" onClose={() => modalsModel.popModal()}>
                <div className="modal-loading">Loading...</div>
            </Modal>
        );
    }

    const renderForm = (submitLabel: string, growCommand?: boolean) => (
        <div className="script-edit-form">
            <div className="script-form-field">
                <label>Name</label>
                <Input value={draftName} onChange={setDraftName} placeholder="Script name" autoFocus />
            </div>
            <div className={`script-form-field ${growCommand ? "form-field-grow" : ""}`}>
                <label>Command</label>
                <textarea
                    className="script-command-textarea"
                    value={draftCommand}
                    onChange={(e) => setDraftCommand(e.target.value)}
                    placeholder="Shell commands (one per line)"
                    rows={growCommand ? undefined : 4}
                />
            </div>
            <div className="script-form-field">
                <label>Description (optional)</label>
                <textarea
                    className="script-desc-textarea"
                    value={draftDesc}
                    onChange={(e) => setDraftDesc(e.target.value)}
                    placeholder="What this script does"
                    rows={2}
                />
            </div>
            {errorMsg && <div className="script-form-error">{errorMsg}</div>}
            <div className="script-form-actions">
                <Button className="grey ghost" onClick={resetForm}>
                    Cancel
                </Button>
                <Button onClick={handleSave}>{submitLabel}</Button>
            </div>
        </div>
    );

    return (
        <Modal className="workspace-scripts-modal" onClose={() => modalsModel.popModal()}>
            <div className="scripts-modal-root" onKeyDown={handleKeyDown}>
                <div className="scripts-modal-header">Workspace Scripts</div>
                <div className="scripts-modal-body">
                    {scripts.length === 0 && !showAddForm ? (
                        <div className="scripts-empty-state">
                            <div className="scripts-empty-icon">
                                <i className="fa-sharp fa-regular fa-scroll"></i>
                            </div>
                            <div className="scripts-empty-text">No scripts yet</div>
                            <div className="scripts-empty-subtext">
                                Save your frequently used commands to quickly invoke them later.
                            </div>
                        </div>
                    ) : (
                        <div className="scripts-list">
                            {scripts.map((script) =>
                                editingId === script.id ? (
                                    <div key={script.id}>{renderForm("Save", false)}</div>
                                ) : (
                                    <div key={script.id} className="script-item">
                                        <div className="script-item-header">
                                            <div className="script-item-name">{script.name}</div>
                                            <div className="script-item-actions">
                                                <button
                                                    className="script-action-btn edit"
                                                    onClick={() => startEdit(script)}
                                                    title="Edit script"
                                                >
                                                    <i className="fa-sharp fa-solid fa-pencil"></i>
                                                </button>
                                                <button
                                                    className="script-action-btn delete"
                                                    onClick={() => handleDelete(script)}
                                                    title="Delete script"
                                                >
                                                    <i className="fa-sharp fa-solid fa-trash"></i>
                                                </button>
                                            </div>
                                        </div>
                                        <div className="script-item-command">{script.command}</div>
                                        {script.desc && <div className="script-item-desc">{script.desc}</div>}
                                    </div>
                                )
                            )}
                            {showAddForm && renderForm("Add Script", false)}
                        </div>
                    )}
                </div>
                {!isEditing && (
                    <div className="scripts-modal-footer">
                        <Button onClick={startAdd}>+ Add Script</Button>
                    </div>
                )}
            </div>
        </Modal>
    );
};

export const WorkspaceScriptsModal = memo(WorkspaceScriptsModalComponent);
WorkspaceScriptsModal.displayName = "WorkspaceScriptsModal";
