// Copyright 2025, Command Line Inc
// SPDX-License-Identifier: Apache-2.0

import { ClientModel } from "@/app/store/client-model";
import { getApi } from "@/store/global";
import * as util from "@/util/util";

class GlobalModel {
    private static instance: GlobalModel;
    static readonly IsActiveThrottleMs = 5000;

    builderId: string;
    platform: NodeJS.Platform;
    lastSetIsActiveTs = 0;

    private constructor() {
        // private constructor for singleton pattern
    }

    static getInstance(): GlobalModel {
        if (!GlobalModel.instance) {
            GlobalModel.instance = new GlobalModel();
        }
        return GlobalModel.instance;
    }

    async initialize(initOpts: GlobalInitOptions): Promise<void> {
        ClientModel.getInstance().initialize(initOpts.clientId);
        this.builderId = initOpts.builderId;
        this.platform = initOpts.platform;
    }

    setIsActive(): void {
        const now = Date.now();
        if (now - this.lastSetIsActiveTs < GlobalModel.IsActiveThrottleMs) {
            return;
        }
        this.lastSetIsActiveTs = now;
        util.fireAndForget(() => getApi().setIsActive());
    }
}

export { GlobalModel };
