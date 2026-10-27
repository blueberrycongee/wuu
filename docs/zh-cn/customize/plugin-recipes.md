# 桌面插件配方

这些示例基于[桌面插件快速上手](desktop-plugin-quickstart.md)。每个 TypeScript 代码块都是完整桌面入口，可作为包中的 `src/index.ts`，并让 `desktop.entry` 指向 `dist/index.js`。

## 向草稿添加审查提示

输入框贡献读取公开应用快照并更新所属草稿。原生输入框保持不变，用户可以检查新增文本后自行发送。

```ts
import type { PluginGenerationApi, UISlotRenderProps } from "@wuu/plugin-sdk";

export function activate(api: PluginGenerationApi): void {
  const React = api.react;
  function ReviewPrompt({ application }: UISlotRenderProps) {
    const snapshot = React.useSyncExternalStore(application.subscribe, application.getSnapshot, application.getSnapshot);
    const [error, setError] = React.useState("");
    const draft = snapshot.drafts[snapshot.drafts.activePane];
    const ownerTabId = draft.ownerTabId;
    async function append() {
      if (!ownerTabId) return;
      try {
        const text = draft.text.trimEnd();
        await application.invoke("composer.set-draft", {
          ownerTabId, text: `${text}${text ? "\n\n" : ""}Review the current changes for concrete bugs.`,
        });
        setError("");
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : String(cause));
      }
    }
    return React.createElement("div", null,
      React.createElement("button", { disabled: !ownerTabId || snapshot.composer.readOnly, onClick: append }, "Add review prompt"),
      error ? React.createElement("p", { role: "alert" }, error) : null);
  }
  api.registerUIContribution({
    id: "review-prompt", target: "wuu.default-composer/composer.above", render: ReviewPrompt,
  });
}
```

应用会验证草稿归属和 generation 生命周期。选中文本后写入草稿也使用同一操作；贡献卸载时移除选择监听器。

## 添加工作区页面

注册 View，再通过 manifest 入口开放。页面的打开操作和周围导航由宿主管理。

```ts
import type { PluginGenerationApi } from "@wuu/plugin-sdk";

export function activate(api: PluginGenerationApi): void {
  api.registerViewType({
    id: "dashboard",
    title: "Dashboard",
    defaultRegion: "auxiliary",
    persistence: "durable",
    render: () => api.react.createElement("p", null, "Workspace dashboard"),
  });
}
```

将下面的贡献合并到包的 `plugin.json`：

```json
{
  "contributes": {
    "workspaceTools": [
      { "id": "dashboard-entry", "view": "dashboard", "title": "Dashboard" }
    ]
  }
}
```

`view` 必须匹配同一插件注册的 ID。设置页使用 `settingsPages`，导航入口使用 `navigation`。只有确实需要初始打开位置时才添加 `registerViewPlacement`，不要把每个可选工具都强制打开在工作台上。

## 添加网站页面

网站复用现有 View 和导航契约。下面是可选的 Notion 示例，只有用户选择其导航入口后才打开页面；将 URL 改为飞书或其他 HTTP(S) 网站，即可用于其他扩展。

```ts
import type { PluginGenerationApi } from "@wuu/plugin-sdk";

export function activate(api: PluginGenerationApi): void {
  const WebPage = api.ui.WebPage as unknown as
    (props: Readonly<Record<string, unknown>>) => unknown;
  api.registerViewType({
    id: "website",
    title: "Notion",
    defaultRegion: "primary",
    persistence: "durable",
    render: () => api.react.createElement(WebPage, {
      url: "https://www.notion.so/",
      title: "Notion",
    }),
  });
}
```

在包的 `plugin.json` 中使用普通导航贡献：

```json
{
  "contributes": {
    "navigation": [
      { "id": "website-entry", "view": "website", "title": "Notion" }
    ]
  }
}
```

对于没有 manifest 的单个本地 TypeScript 文件，可在激活时添加 `api.registerViewPlacement({ id: "website-entry", view: "website", region: "primary" })`，打开同一个 View。其 Ribbon 入口、布局、浏览器会话和清理由宿主管理。网站与 Wuu 其他浏览器页面共用已导入的 Cookie；会话中没有有效登录时，需要重新登录。普通的工作台视图切换只会隐藏网页，并保留同一个页面。显式关闭 View、卸载 WebPage 组件、禁用插件或重新加载其 generation 会关闭页面与弹窗。此配方不会安装任何网站。兼容旧宿主时可检查 `typeof api.ui.WebPage === "function"`。此组件仅支持桌面主工作台窗口，不支持弹出的会话窗口或远程 Web／移动客户端。非桌面客户端显示浏览器不可用状态。

## 连接页面和运行时

`api.invokeRuntime(method, input, { workspaceId })` 调用同一插件当前活动的运行时 generation。运行时需要实现 `plugin.client.request` 能力；方法名是插件自己的分派键，不是任意 app-server 方法。通过 `listWorkspaces` 检查工作区可用性，在 UI 中处理请求失败；需要随变化刷新视图时，使用 `onHostEvent`。

持久业务状态应放在运行时存储中，不要在 renderer 和运行时维护两份独立副本。订阅和定时器需要注册清理。临时进度可以使用 `showConversationCard`，但它的 `update`、`dismiss`、`dispose` 方法不会让卡片成为保存的会话历史。

## 改变外观，保留行为

颜色和语法高亮可通过 `contributes.themes` 声明主题，见[主题与设置](themes-settings.md)和[主题契约](theme-surface-matrix.md)。工具结果样式应区分正文 renderer 与执行摘要 presenter，不要重复输出宿主已经放置的结果正文。

分发前应构建并验证包，再在 Wuu Desktop 中检查动作、错误状态、键盘焦点、窄布局、两种主题、较大字号、禁用和重载。`wuu plugin test` 不执行这些渲染验收。
