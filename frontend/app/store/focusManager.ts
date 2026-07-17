// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { waveAIHasFocusWithin } from "@/app/aipanel/waveai-focus-utils";
import { WaveAIModel } from "@/app/aipanel/waveai-model";
import { getBlockComponentModel, getBlockMetaKeyAtom } from "@/app/store/global";
import { globalStore } from "@/app/store/jotaiStore";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { termLog } from "@/util/termlog";
import { fireAndForget } from "@/util/util";
import { getLayoutModelForCurrentTab } from "@/layout/index";
import { focusedBlockId } from "@/util/focusutil";
import { Atom, atom, type PrimitiveAtom } from "jotai";

export type FocusStrType = "node" | "waveai";

export class FocusManager {
    private static instance: FocusManager | null = null;

    focusType: PrimitiveAtom<FocusStrType> = atom("node");
    blockFocusAtom: Atom<string | null>;

    private constructor() {
        this.blockFocusAtom = atom((get) => {
            if (get(this.focusType) == "waveai") {
                return null;
            }
            const layoutModel = getLayoutModelForCurrentTab();
            const lnode = get(layoutModel.focusedNode);
            return lnode?.data?.blockId;
        });

        let prevBlockId: string | null = null;
        globalStore.sub(this.blockFocusAtom, () => {
            const blockId = globalStore.get(this.blockFocusAtom);
            if (blockId && blockId !== prevBlockId) {
                prevBlockId = blockId;
                try {
                    const daemonId = globalStore.get(getBlockMetaKeyAtom(blockId, "session:daemonid"));
                    if (daemonId) {
                        fireAndForget(() => RpcApi.RecordSessionActivityCommand(TabRpcClient, { daemonid: daemonId }));
                    }
                } catch (_) {}
            } else if (!blockId) {
                prevBlockId = null;
            }
        });
    }

    static getInstance(): FocusManager {
        if (!FocusManager.instance) {
            FocusManager.instance = new FocusManager();
        }
        return FocusManager.instance;
    }

    setWaveAIFocused(force: boolean = false) {
        const isAlreadyFocused = globalStore.get(this.focusType) == "waveai";
        if (!force && isAlreadyFocused) {
            return;
        }
        termLog("[focus]", "setWaveAIFocused");
        globalStore.set(this.focusType, "waveai");
        this.refocusNode();
    }

    setBlockFocus(force: boolean = false) {
        const ftype = globalStore.get(this.focusType);
        if (!force && ftype == "node") {
            return;
        }
        termLog("[focus]", "setBlockFocus");
        globalStore.set(this.focusType, "node");
        this.refocusNode();
    }

    waveAIFocusWithin(): boolean {
        return waveAIHasFocusWithin();
    }

    nodeFocusWithin(): boolean {
        return focusedBlockId() != null;
    }

    requestNodeFocus(): void {
        termLog("[focus]", "requestNodeFocus");
        globalStore.set(this.focusType, "node");
    }

    requestWaveAIFocus(): void {
        termLog("[focus]", "requestWaveAIFocus");
        globalStore.set(this.focusType, "waveai");
    }

    getFocusType(): FocusStrType {
        return globalStore.get(this.focusType);
    }

    refocusNode() {
        const ftype = globalStore.get(this.focusType);
        if (ftype == "waveai") {
            termLog("[focus]", "refocusNode: waveai");
            WaveAIModel.getInstance().focusInput();
            return;
        }
        const layoutModel = getLayoutModelForCurrentTab();
        const lnode = globalStore.get(layoutModel.focusedNode);
        if (lnode == null || lnode.data?.blockId == null) {
            return;
        }
        const blockId = lnode.data.blockId;
        termLog("[focus]", "refocusNode", blockId);
        layoutModel.focusNode(lnode.id);
        const bcm = getBlockComponentModel(blockId);
        const ok = bcm?.viewModel?.giveFocus?.();
        if (!ok) {
            const inputElem = document.getElementById(`${blockId}-dummy-focus`);
            inputElem?.focus();
        }
    }
}
