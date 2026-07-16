// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { App } from "@/app/app";
import { loadMonaco } from "@/app/monaco/monaco-env";
import { loadBadges } from "@/app/store/badge";
import { GlobalModel } from "@/app/store/global-model";
import {
    globalRefocus,
    registerBuilderGlobalKeys,
    registerControlShiftStateUpdateHandler,
    registerElectronReinjectKeyHandler,
    registerGlobalKeys,
} from "@/app/store/keymodel";
import { modalsModel } from "@/app/store/modalmodel";
import { RpcApi } from "@/app/store/wshclientapi";
import { makeBuilderRouteId, makeTabRouteId } from "@/app/store/wshrouter";
import { initWshrpc, TabRpcClient } from "@/app/store/wshrpcutil";
import { BuilderApp } from "@/builder/builder-app";
import { getLayoutModelForStaticTab } from "@/layout/index";
import { countersClear, countersPrint } from "@/store/counters";
import {
    atoms,
    getApi,
    globalStore,
    initGlobal,
    initGlobalWaveEventSubs,
    loadConnStatus,
    subscribeToConnEvents,
} from "@/store/global";
import { activeTabIdAtom } from "@/store/tab-model";
import * as WOS from "@/store/wos";
import { loadFonts } from "@/util/fontutil";
import { setKeyUtilPlatform } from "@/util/keyutil";
import { isMacOS, setMacOSVersion } from "@/util/platformutil";
import { createElement } from "react";
import { createRoot } from "react-dom/client";

const platform = getApi().getPlatform();
document.title = `Wave Terminal`;
let savedInitOpts: WaveInitOpts = null;

type WorkspaceContext = {
    ownTabId: string;
    activeTabId: string;
    client: Client;
    waveWindow: WaveWindow;
    workspace: Workspace;
    activeTab: Tab;
    layout: LayoutState;
    workspaceTabs: Tab[];
    workspaceLayouts: LayoutState[];
};

class WorkspaceSubscription {
    private _unsubscribe: (() => void) | null = null;

    setWorkspace(workspaceId: string) {
        this._unsubscribe?.();
        if (workspaceId != null) {
            this._unsubscribe = WOS.wpsSubscribeToObject(WOS.makeORef("workspace", workspaceId));
        } else {
            this._unsubscribe = null;
        }
    }

    dispose() {
        this._unsubscribe?.();
        this._unsubscribe = null;
    }
}

const workspaceSubscription = new WorkspaceSubscription();

function waitForNextPaint(): Promise<void> {
    return Promise.race([
        new Promise<void>((resolve) => {
            requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
        }),
        new Promise<void>((resolve) => setTimeout(resolve, 50)),
    ]);
}

(window as any).WOS = WOS;
(window as any).globalStore = globalStore;
(window as any).globalAtoms = atoms;
(window as any).RpcApi = RpcApi;
(window as any).isFullScreen = false;
(window as any).countersPrint = countersPrint;
(window as any).countersClear = countersClear;
(window as any).getLayoutModelForStaticTab = getLayoutModelForStaticTab;
(window as any).modalsModel = modalsModel;

async function loadWorkspaceContext(opts: {
    clientId: string;
    windowId: string;
    ownTabId: string;
}): Promise<WorkspaceContext | null> {
    const client = await WOS.reloadWaveObject<Client>(WOS.makeORef("client", opts.clientId));
    if (client == null) {
        return null;
    }

    const waveWindow = await WOS.reloadWaveObject<WaveWindow>(WOS.makeORef("window", opts.windowId));
    if (waveWindow == null) {
        return null;
    }

    const workspace = await WOS.reloadWaveObject<Workspace>(WOS.makeORef("workspace", waveWindow.workspaceid));
    if (workspace == null) {
        return null;
    }

    const activeTabId = workspace.activetabid || opts.ownTabId;
    const activeTab = await WOS.reloadWaveObject<Tab>(WOS.makeORef("tab", activeTabId));
    if (activeTab == null) {
        return null;
    }

    const layout = await WOS.reloadWaveObject<LayoutState>(WOS.makeORef("layout", activeTab.layoutstate));
    if (layout == null) {
        return null;
    }

    const workspaceTabs = await Promise.all(
        workspace.tabids.map((tabId) => WOS.reloadWaveObject<Tab>(WOS.makeORef("tab", tabId)))
    );

    const workspaceLayouts = await Promise.all(
        workspaceTabs
            .filter((tab) => tab?.layoutstate)
            .map((tab) => WOS.reloadWaveObject<LayoutState>(WOS.makeORef("layout", tab.layoutstate)))
    );

    return {
        ownTabId: opts.ownTabId,
        activeTabId,
        client,
        waveWindow,
        workspace,
        activeTab,
        layout,
        workspaceTabs,
        workspaceLayouts,
    };
}

function applyWorkspaceContext(ctx: WorkspaceContext, opts: { tabContext: "own" | "active" }) {
    const tabIdForRenderer = opts.tabContext === "active" ? ctx.activeTabId : ctx.ownTabId;

    globalStore.set(atoms.workspaceId, ctx.workspace.oid);
    globalStore.set(activeTabIdAtom, ctx.activeTabId);
    globalStore.set(atoms.staticTabId, tabIdForRenderer);
    globalStore.set(atoms.updaterStatusAtom, getApi().getUpdaterStatus());
}

function updateZoomFactor(zoomFactor: number) {
    console.log("update zoomfactor", zoomFactor);
    document.documentElement.style.setProperty("--zoomfactor", String(zoomFactor));
    document.documentElement.style.setProperty("--zoomfactor-inv", String(1 / zoomFactor));
}

async function initBare() {
    getApi().sendLog("Init Bare");
    document.body.style.visibility = "hidden";
    document.body.style.opacity = "0";
    document.body.classList.add("is-transparent");
    getApi().onWaveInit(initWaveWrap);
    getApi().onBuilderInit(initBuilderWrap);
    setKeyUtilPlatform(platform);
    loadFonts();
    updateZoomFactor(getApi().getZoomFactor());
    getApi().onZoomFactorChange((zoomFactor) => {
        updateZoomFactor(zoomFactor);
    });
    document.fonts.ready.then(() => {
        console.log("Init Bare Done");
        getApi().setWindowInitStatus("ready");
    });
}

document.addEventListener("DOMContentLoaded", initBare);

async function initWaveWrap(initOpts: WaveInitOpts) {
    try {
        if (savedInitOpts) {
            globalStore.set(activeTabIdAtom, initOpts.tabId);
            globalStore.set(atoms.staticTabId, initOpts.tabId);
            await reinitWave();
            return;
        }
        savedInitOpts = initOpts;
        await initWave(initOpts);
    } catch (e) {
        getApi().sendLog("Error in initWave " + e.message + "\n" + e.stack);
        console.error("Error in initWave", e);
    } finally {
        document.body.style.visibility = null;
        document.body.style.opacity = null;
        document.body.classList.remove("is-transparent");
        document.body.classList.remove("init");
    }
}

async function reinitWave() {
    console.log("Reinit Wave");
    getApi().sendLog("Reinit Wave");

    // We use this hack to prevent a flicker of the previously-hovered tab when this view was last active.
    document.body.classList.add("nohover");
    requestAnimationFrame(() =>
        setTimeout(() => {
            document.body.classList.remove("nohover");
        }, 100)
    );

    const ctx = await loadWorkspaceContext({
        clientId: savedInitOpts.clientId,
        windowId: savedInitOpts.windowId,
        ownTabId: savedInitOpts.tabId,
    });
    if (ctx == null) {
        return;
    }

    applyWorkspaceContext(ctx, { tabContext: "active" });
    globalStore.set(atoms.reinitVersion, globalStore.get(atoms.reinitVersion) + 1);
    workspaceSubscription.setWorkspace(ctx.workspace.oid);
    document.title = `Wave Terminal - ${ctx.activeTab.name}`;

    await waitForNextPaint();
    getApi().setWindowInitStatus("wave-ready");

    setTimeout(() => {
        globalRefocus();
    }, 50);
}

async function initWave(initOpts: WaveInitOpts) {
    getApi().sendLog("Init Wave " + JSON.stringify(initOpts));
    const globalInitOpts: GlobalInitOptions = {
        tabId: initOpts.tabId,
        clientId: initOpts.clientId,
        windowId: initOpts.windowId,
        platform,
        environment: "renderer",
        primaryTabStartup: initOpts.primaryTabStartup,
    };
    console.log("Wave Init", globalInitOpts);
    globalStore.set(activeTabIdAtom, initOpts.tabId);
    await GlobalModel.getInstance().initialize(globalInitOpts);
    initGlobal(globalInitOpts);
    (window as any).globalAtoms = atoms;

    // Init WPS event handlers
    const globalWS = initWshrpc(makeTabRouteId(initOpts.tabId));
    (window as any).globalWS = globalWS;
    (window as any).TabRpcClient = TabRpcClient;

    // ensures client/window/workspace are loaded into the cache before rendering
    try {
        await loadConnStatus();
        await loadBadges();
        initGlobalWaveEventSubs(initOpts);
        subscribeToConnEvents();
        if (isMacOS()) {
            const macOSVersion = await RpcApi.MacOSVersionCommand(TabRpcClient);
            setMacOSVersion(macOSVersion);
        }
        const ctx = await loadWorkspaceContext({
            clientId: initOpts.clientId,
            windowId: initOpts.windowId,
            ownTabId: initOpts.tabId,
        });
        if (ctx != null) {
            applyWorkspaceContext(ctx, { tabContext: "own" });
            workspaceSubscription.setWorkspace(ctx.workspace.oid);
            document.title = `Wave Terminal - ${ctx.activeTab.name}`;
        }
    } catch (e) {
        console.error("Failed initialization error", e);
        getApi().sendLog("Error in initialization (wave.ts, loading required objects) " + e.message + "\n" + e.stack);
    }
    registerGlobalKeys();
    registerElectronReinjectKeyHandler();
    registerControlShiftStateUpdateHandler();
    await loadMonaco();
    const fullConfig = await RpcApi.GetFullConfigCommand(TabRpcClient);
    console.log("fullconfig", fullConfig);
    globalStore.set(atoms.fullConfigAtom, fullConfig);
    const waveaiModeConfig = await RpcApi.GetWaveAIModeConfigCommand(TabRpcClient);
    globalStore.set(atoms.waveaiModeConfigAtom, waveaiModeConfig.configs);
    console.log("Wave First Render");
    let firstRenderResolveFn: () => void = null;
    const firstRenderPromise = new Promise<void>((resolve) => {
        firstRenderResolveFn = resolve;
    });
    const reactElem = createElement(App, { onFirstRender: firstRenderResolveFn }, null);
    const elem = document.getElementById("main");
    const root = createRoot(elem);
    root.render(reactElem);
    await firstRenderPromise;
    console.log("Wave First Render Done");
    getApi().setWindowInitStatus("wave-ready");
}

async function initBuilderWrap(initOpts: BuilderInitOpts) {
    try {
        await initBuilder(initOpts);
    } catch (e) {
        getApi().sendLog("Error in initBuilder " + e.message + "\n" + e.stack);
        console.error("Error in initBuilder", e);
    } finally {
        document.body.style.visibility = null;
        document.body.style.opacity = null;
        document.body.classList.remove("is-transparent");
    }
}

async function initBuilder(initOpts: BuilderInitOpts) {
    getApi().sendLog("Init Builder " + JSON.stringify(initOpts));
    const globalInitOpts: GlobalInitOptions = {
        clientId: initOpts.clientId,
        windowId: initOpts.windowId,
        platform,
        environment: "renderer",
        builderId: initOpts.builderId,
    };
    console.log("Tsunami Builder Init", globalInitOpts);
    await GlobalModel.getInstance().initialize(globalInitOpts);
    initGlobal(globalInitOpts);
    (window as any).globalAtoms = atoms;

    const globalWS = initWshrpc(makeBuilderRouteId(initOpts.builderId));
    (window as any).globalWS = globalWS;
    (window as any).TabRpcClient = TabRpcClient;
    await loadConnStatus();

    let appIdToUse: string = null;
    try {
        const oref = WOS.makeORef("builder", initOpts.builderId);
        const rtInfo = await RpcApi.GetRTInfoCommand(TabRpcClient, { oref });
        if (rtInfo && rtInfo["builder:appid"]) {
            appIdToUse = rtInfo["builder:appid"];
        }
    } catch (e) {
        console.log("Could not load saved builder appId from rtinfo:", e);
    }

    document.title = appIdToUse ? `WaveApp Builder (${appIdToUse})` : "WaveApp Builder";

    globalStore.set(atoms.builderAppId, appIdToUse);

    const _client = await WOS.loadAndPinWaveObject<Client>(WOS.makeORef("client", initOpts.clientId));

    registerBuilderGlobalKeys();
    registerElectronReinjectKeyHandler();
    await loadMonaco();
    const fullConfig = await RpcApi.GetFullConfigCommand(TabRpcClient);
    console.log("fullconfig", fullConfig);
    globalStore.set(atoms.fullConfigAtom, fullConfig);
    const waveaiModeConfig = await RpcApi.GetWaveAIModeConfigCommand(TabRpcClient);
    globalStore.set(atoms.waveaiModeConfigAtom, waveaiModeConfig.configs);

    console.log("Tsunami Builder First Render");
    let firstRenderResolveFn: () => void = null;
    const firstRenderPromise = new Promise<void>((resolve) => {
        firstRenderResolveFn = resolve;
    });
    const reactElem = createElement(BuilderApp, { initOpts, onFirstRender: firstRenderResolveFn }, null);
    const elem = document.getElementById("main");
    const root = createRoot(elem);
    root.render(reactElem);
    await firstRenderPromise;
    console.log("Tsunami Builder First Render Done");
}
