// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { MessageModal } from "@/app/modals/messagemodal";
import { NewInstallOnboardingModal } from "@/app/onboarding/onboarding";
import { UpgradeOnboardingModal } from "@/app/onboarding/onboarding-upgrade";
import { UpgradeOnboardingPatch } from "@/app/onboarding/onboarding-upgrade-patch";
import { DeleteFileModal, PublishAppModal, RenameFileModal } from "@/builder/builder-apppanel";
import { SetSecretDialog } from "@/builder/tabs/builder-secrettab";
import { AboutModal } from "./about";
import { ConfirmModal } from "./confirmmodal";
import { UserInputModal } from "./userinputmodal";
import { WorkspaceScriptsModal } from "./workspace-scripts-modal";
import { WorkspaceScriptsPicker } from "./workspace-scripts-picker";

const modalRegistry: { [key: string]: React.ComponentType<any> } = {
    [NewInstallOnboardingModal.displayName || "NewInstallOnboardingModal"]: NewInstallOnboardingModal,
    [UpgradeOnboardingModal.displayName || "UpgradeOnboardingModal"]: UpgradeOnboardingModal,
    [UpgradeOnboardingPatch.displayName || "UpgradeOnboardingPatch"]: UpgradeOnboardingPatch,
    [UserInputModal.displayName || "UserInputModal"]: UserInputModal,
    [AboutModal.displayName || "AboutModal"]: AboutModal,
    [MessageModal.displayName || "MessageModal"]: MessageModal,
    [ConfirmModal.displayName || "ConfirmModal"]: ConfirmModal,
    [PublishAppModal.displayName || "PublishAppModal"]: PublishAppModal,
    [RenameFileModal.displayName || "RenameFileModal"]: RenameFileModal,
    [DeleteFileModal.displayName || "DeleteFileModal"]: DeleteFileModal,
    [SetSecretDialog.displayName || "SetSecretDialog"]: SetSecretDialog,
    [WorkspaceScriptsPicker.displayName || "WorkspaceScriptsPicker"]: WorkspaceScriptsPicker,
    [WorkspaceScriptsModal.displayName || "WorkspaceScriptsModal"]: WorkspaceScriptsModal,
};

export const getModalComponent = (key: string): React.ComponentType<any> | undefined => {
    return modalRegistry[key];
};
