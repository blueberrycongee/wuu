# 桌面插件配方

这些示例基于[桌面插件快速上手](desktop-plugin-quickstart.md)。每个 TypeScript 代码块都是完整桌面入口，可作为包中的 `src/index.ts`，并让 `desktop.entry` 指向 `dist/index.js`。

## 向草稿添加审查提示

输入框 Presenter 可以读取公开的草稿快照并调用宿主动作。本例使用包装模式保留原生输入框，让用户检查新增文本后自行发送。

```ts
import type { ComposerSnapshotV1, PluginGenerationApi, PresenterProps } from "@wuu/plugin-sdk";

export function activate(api: PluginGenerationApi): void {
  const React = api.react;
  const Button = api.ui.Button as unknown as
    (props: Readonly<Record<string, unknown>>) => unknown;
  const action = "conversation.composer.set-draft";

  function ReviewPrompt(input: Readonly<Record<string, unknown>>) {
    const props = input.presenter as PresenterProps;
    const snapshot = props.snapshot as ComposerSnapshotV1;
    const [error, setError] = React.useState("");
    const disabled = snapshot.readOnly || !props.host.actions.includes(action);
    async function append() {
      try {
        const draft = snapshot.draftText?.trimEnd() ?? "";
        await props.host.invoke(action,
          `${draft}${draft ? "\n\n" : ""}Review the current changes for concrete bugs.`);
        setError("");
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : String(cause));
      }
    }
    return React.createElement("div", null,
      props.fallback,
      React.createElement(Button, { disabled, onClick: append }, "Add review prompt"),
      error ? React.createElement("p", { role: "alert" }, error) : null);
  }

  api.registerPresenter({
    id: "review-prompt", target: "conversation.composer", mode: "wrap",
    render: props => React.createElement(ReviewPrompt, { presenter: props }),
  });
}
```

动作接受字符串，不接受 DOM 事件或 textarea 引用，宿主仍会检查只读状态。实现“将选区加入草稿”时，可通过浏览器 Selection API 获取纯文本，确认选区属于公开的消息锚点，再通过相同动作传入文本。卸载时移除选区监听器，点击时保留用户选区，并处理滚动和视口边缘。

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

## 连接页面和运行时

`api.invokeRuntime(method, input, { workspaceId })` 调用同一插件当前活动的运行时 generation。运行时需要实现 `plugin.client.request` 能力；方法名是插件自己的分派键，不是任意 app-server 方法。通过 `listWorkspaces` 检查工作区可用性，在 UI 中处理请求失败；需要随变化刷新视图时，使用 `onHostEvent`。

持久业务状态应放在运行时存储中，不要在 renderer 和运行时维护两份独立副本。订阅和定时器需要注册清理。临时进度可以使用 `showConversationCard`，但它的 `update`、`dismiss`、`dispose` 方法不会让卡片成为保存的会话历史。

## 改变外观，保留行为

颜色和语法高亮可通过 `contributes.themes` 声明主题，见[主题与设置](themes-settings.md)和[主题契约](theme-surface-matrix.md)。工具结果样式应区分正文 renderer 与执行摘要 presenter，不要重复输出宿主已经放置的结果正文。

分发前应构建并验证包，再在 Wuu Desktop 中检查动作、错误状态、键盘焦点、窄布局、两种主题、较大字号、禁用和重载。`wuu plugin test` 不执行这些渲染验收。
