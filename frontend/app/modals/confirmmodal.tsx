// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { Modal } from "@/app/modals/modal";
import { modalsModel } from "@/app/store/modalmodel";

const ConfirmModal = ({ message, onOk }: { message: string; onOk?: () => void }) => {
    const handleOk = () => {
        modalsModel.popModal();
        if (onOk) onOk();
    };
    const handleClose = () => {
        modalsModel.popModal();
    };

    return (
        <Modal className="message-modal" onOk={handleOk} onClose={handleClose} okLabel="Confirm" cancelLabel="Cancel">
            <div className="p-1 text-sm">{message}</div>
        </Modal>
    );
};

ConfirmModal.displayName = "ConfirmModal";

export { ConfirmModal };
