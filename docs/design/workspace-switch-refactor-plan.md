# Workspace Switch 重构方案

## 背景

当前 workspace 切换问题的根因不是单个条件判断，而是 BrowserView 生命周期、后端 window/workspace 状态、前端 React/Jotai 渲染状态之间缺少清晰边界。

workspace switch 同时涉及三件事：

- Go 后端更新 window 所属 workspace。
- Electron 主进程选择、挂载、定位目标 tab 的 BrowserView。
- Renderer 重新绑定当前 workspace/tab 上下文并刷新 UI。

这些步骤目前主要依赖调用顺序和隐式状态更新，没有显式生命周期合约。因此当 BrowserView 已经正确复用并上屏时，renderer 仍可能读到旧的 workspace/tab atom，表现为 UI 空白。

## 已暴露的问题

### 1. 异步加载边界不显式

原来的 `reloadAllWorkspaceTabs` 使用 `forEach` fire-and-forget：

```ts
ws.tabids?.forEach((tabid) => {
    WOS.reloadWaveObject<Tab>(WOS.makeORef("tab", tabid));
});
```

`reinitWave` 继续向下执行时，tab/layout 数据可能尚未返回。React 重渲染时可能读到 `null` 或 stale atom value。

这类加载应该使用 `await Promise.all(...)` 明确等待。

### 2. lifecycle state 通过 derived atom 间接传播

原来的 `workspaceIdAtom` 从 window WOS object 派生：

```ts
const workspaceIdAtom = atom((get) => {
    const windowData = WOS.getObjectValue<WaveWindow>(WOS.makeORef("window", get(windowIdAtom)), get);
    return windowData?.workspaceid ?? null;
});
```

这条链路是：

```text
RPC 返回
  -> WOS cache set atom
  -> Jotai derived atom 重算
  -> workspace atom 重算
  -> React component 重渲染
```

对展示型派生值这可以接受，但对 workspace switch 这种生命周期入口状态，链路太隐式，调试困难，也容易受异步时序影响。

### 3. init 和 reinit 逻辑分叉

`initWave` 和 `reinitWave` 都需要加载：

- client
- window
- workspace
- active tab
- layout
- workspace tabs/layouts

但两条路径各自实现。后续新增 workspace context 状态时，很容易只改一边，另一边漏掉。

这次问题里，reinit 路径就缺少了对 `workspaceId`、`activeTabId/staticTabId`、workspace subscription 的完整同步。

### 4. 没有 renderer reinit 完成合约

首次初始化有 `onFirstRender` promise，但 reinit 只是更新 atom 后返回。

数据加载完成、Jotai atom 更新、React commit、DOM 实际更新是不同阶段。当前没有明确的 `reinit-ready` 或 render ack，主进程只能假设 renderer 已经完成。

### 5. BrowserView 生命周期和 renderer context 混在一起

workspace switch 的性能目标是复用 BrowserView/xterm。主进程的目标是“把目标 tab view 从 cache 取回并上屏”。

但 renderer 同时承载：

- tab 内容：layout、blocks、xterm
- workspace chrome：tab bar、workspace switcher、widgets
- window context：windowId、workspaceId、activeTabId、uiContext、WPS subscription

这导致 BrowserView 保活后，renderer 仍必须正确刷新 workspace chrome 和 window context。若这些状态没有显式 rebind，就会出现 BrowserView 正常但 UI 空白的情况。

## 重构目标

短期目标：

- 不改变现有 UI 架构。
- 保持 BrowserView/xterm 在 workspace switch 时不销毁。
- 让 workspace switch 的异步边界和状态 owner 显式化。
- 减少 init/reinit 复制粘贴。

长期目标：

- 明确区分 window-scoped chrome 和 tab-scoped content。
- 让 workspace switch 成为可观测的状态机。

## 第一阶段：收敛现有架构

### 1. 抽公共 `loadWorkspaceContext`

将 `initWave` 和 `reinitWave` 的共享加载逻辑集中到一个函数。

```ts
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
```

这里 `ownTabId` 和 `activeTabId` 必须区分：

- `ownTabId`：当前 BrowserView/WebContents 绑定的 tab，即 `wave-init` 传入的 `initOpts.tabId`。
- `activeTabId`：当前 workspace 选中的 tab，即 `workspace.activetabid`。

init 场景通常二者一致，但设计上不应混用。reinit 场景需要明确判断当前 BrowserView 是否应该绑定到 workspace active tab，还是只刷新 workspace chrome。

### 2. 抽公共 `applyWorkspaceContext`

所有 Jotai context 更新集中到一个函数，避免 init/reinit 漏项。

```ts
function applyWorkspaceContext(ctx: WorkspaceContext, opts: { tabContext: "own" | "active" }) {
    const tabIdForRenderer = opts.tabContext === "active" ? ctx.activeTabId : ctx.ownTabId;

    globalStore.set(atoms.workspaceId, ctx.workspace.oid);
    globalStore.set(activeTabIdAtom, ctx.activeTabId);
    globalStore.set(atoms.staticTabId, tabIdForRenderer);
    globalStore.set(atoms.updaterStatusAtom, getApi().getUpdaterStatus());
    globalStore.set(atoms.reinitVersion, globalStore.get(atoms.reinitVersion) + 1);
}
```

调用方必须显式选择 tab context：

- 首次 init：`tabContext: "own"`，因为当前 BrowserView 绑定的是 `initOpts.tabId`。
- workspace reinit：若当前 BrowserView 已被主进程选为目标 workspace active tab，则使用 `tabContext: "active"`。

这个选择不能隐藏在 `applyWorkspaceContext` 内部，否则会再次模糊 BrowserView identity 和 workspace active tab 的区别。

### 3. 引入 workspace subscription manager

避免在 `wave.ts` 中散落 unsubscribe/re-subscribe 逻辑。

```ts
class WorkspaceSubscription {
    unsubscribe: () => void = null;

    setWorkspace(workspaceId: string) {
        this.unsubscribe?.();
        this.unsubscribe = WOS.wpsSubscribeToObject(WOS.makeORef("workspace", workspaceId));
    }

    dispose() {
        this.unsubscribe?.();
        this.unsubscribe = null;
    }
}
```

### 4. 定义 reinit lifecycle

reinit 不应只是“收到 wave-init 后随手 set 一些 atom”，而应有明确阶段。

```ts
async function rebindWorkspaceContext(initOpts: WaveInitOpts) {
    const ctx = await loadWorkspaceContext({
        clientId: initOpts.clientId,
        windowId: initOpts.windowId,
        ownTabId: initOpts.tabId,
    });
    if (ctx == null) {
        return;
    }

    applyWorkspaceContext(ctx, { tabContext: "active" });
    workspaceSubscription.setWorkspace(ctx.workspace.oid);

    await waitForNextPaint();
    getApi().setWindowInitStatus("wave-ready");
}
```

`waitForNextPaint` 不是浏览器内置 API，应在代码中显式实现。对 reinit 场景建议使用双 `requestAnimationFrame`，给 React/Jotai 一次 commit 和浏览器一次绘制机会：

```ts
function waitForNextPaint(): Promise<void> {
    return new Promise((resolve) => {
        requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
    });
}
```

建议阶段：

```text
load workspace context
  -> apply explicit atoms
  -> update subscriptions
  -> wait render tick / render ack
  -> mark ready
```

## 第二阶段：明确 state owner

将状态分为三类。

### Explicit lifecycle atoms

这些状态由生命周期事件显式写入：

- `windowId`
- `workspaceId`
- `activeTabId`
- `staticTabId` 或后续重命名后的 tab context id
- `reinitVersion`

workspace switch、tab switch、renderer reinit 都应显式更新这些 atom。

### Derived display atoms

这些状态可以继续 derived：

- `workspace`
- `settings`
- `hasConfigErrors`
- `uiContext`

但 derived atom 不应承担 workspace switch 的主状态来源。

### WOS object atoms

WOS 只负责 object cache 和 object update，不承担 UI lifecycle 判断。

生命周期代码应该先 `await` 关键对象加载，再显式更新 lifecycle atoms。

## 第三阶段：整理 tab id 语义

当前 `staticTabId` 名字已经不完全准确。

原语义：

```text
这个 renderer 永远代表某个固定 tab
```

现在实际语义：

```text
当前 renderer context 使用的 active tab id
```

建议后续拆分或重命名：

- `rendererTabId`：如果某个 WebContents 真的固定绑定 tab。
- `activeTabId`：当前 workspace active tab。
- `uiContext.activetabid`：RPC 调用使用的当前 active tab。

如果继续复用同一个 renderer context，则不要再叫 `staticTabId`，避免后续维护者误判它不应该更新。

## 第四阶段：main 进程 workspace switch 状态机

将 `processActionQueue` 中 workspace switch 拆成命名步骤。

### BrowserView 生命周期原则

workspace switch 需要保留 BrowserView/WebContents/xterm，但不应依赖把 view 移到极端 off-screen 坐标来隐藏。`positionTabOffScreen(-15000, -15000)` 可能触发 Electron/WebContentsView 渲染管线异常或 repaint 不稳定。

建议语义：

- `destroy()`：释放 WebContents/xterm。只用于 close tab、close window、cache eviction。
- `removeChildView()`：从当前 window view tree 摘下，保留 WebContentsView 和 WebContents。用于 workspace switch 隐藏旧 workspace views。
- `addChildView()`：切回或复用时重新挂载 cached view。
- `setBounds()`：只负责当前 active view 的尺寸和位置。

也就是说，workspace switch 应该是：

```text
remove old workspace views from view tree
  -> keep them alive in wcvCache
  -> add target active tab view back to view tree
  -> set bounds/focus
```

而不是：

```text
move old views to very large negative coordinates
```

### 状态机草图

```ts
async switchWorkspaceInWindow(workspaceId: string) {
    const previousViews = this.allLoadedTabViews;
    const newWorkspace = await WindowService.SwitchWorkspace(this.waveWindowId, workspaceId);

    this.detachLoadedTabViews(previousViews);
    this.workspaceId = newWorkspace.oid;
    this.allLoadedTabViews = new Map();

    const tabView = await this.attachWorkspaceActiveTab(newWorkspace.activetabid);
    await this.reinitTabView(tabView);

    this.markWorkspaceSwitchReady(newWorkspace.oid, tabView.waveTabId);
}
```

建议阶段日志：

```text
switchworkspace:start
switchworkspace:backend-ready
switchworkspace:previous-views-detached
switchworkspace:view-attached
switchworkspace:renderer-reinit-sent
switchworkspace:ready
```

日志应打印 ids 和 counts，不打印完整 workspace 大对象。

### 保留 action queue 压缩语义

当前 `processActionQueue` 不是普通 FIFO。它有一个重要行为：

```text
actionQueue[0] = 正在执行的 action
actionQueue[1] = 最后一个待执行 action
```

当队列已有待执行 action 时，新 action 会覆盖 `actionQueue[1]`。这能压缩快速连续点击 workspace/tab 的场景，避免执行过期切换。

重构时必须明确保留或重新定义这条语义：

- 正在执行的 action 不被打断。
- pending slot 只保留最后一次 switch tab/workspace。
- create/close tab 是否允许被覆盖需要单独定义，不能隐式混用同一规则。
- 状态机拆分不能把这套 coalescing 行为意外改成普通 FIFO。

## 后续独立 RFC：window chrome / tab content 拆分

长期更干净的结构是拆分：

### Window chrome renderer

负责：

- workspace switcher
- tab bar
- widgets/sidebar
- command palette/global UI
- window settings

### Tab content BrowserViews

负责：

- layout
- blocks
- xterm
- tab-scoped model

workspace switch 时：

```text
window chrome 更新 workspaceId/tabids
tab content manager 选择目标 active tab BrowserView
tab content renderer 不参与 workspace chrome reinit
```

这能从根上避免“tab renderer 同时负责 workspace chrome”的耦合，但改动面较大，不建议作为第一步。

这部分不纳入本文档的实施阶段。若要推进，应单独写 RFC，讨论多 WebContentsView 架构、focus routing、IPC 边界、窗口 resize、context menu、drag/drop、shortcut ownership 等问题。

## 推荐实施顺序

1. 抽 `loadWorkspaceContext` 和 `applyWorkspaceContext`。
2. 引入 workspace subscription manager。
3. 给 reinit 增加轻量 render-ready ack。
4. 将 main 进程 workspace switch 拆成命名步骤和阶段日志。
5. 重命名或拆分 `staticTabId` 语义。
6. 保留并显式测试 action queue coalescing 语义。

## 设计原则

- 异步边界显式：关键数据加载必须 `await Promise.all`。
- 生命周期入口状态显式：`workspaceId`、`activeTabId` 手动 set。
- derived atom 只做展示派生，不做 lifecycle owner。
- BrowserView switch 不等于 renderer full init。
- workspace switch 应是 window/workspace context rebind + tab content view selection。
- ready 状态应有明确合约，不能只依赖 React/Jotai 的隐式调度。

