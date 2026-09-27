# 桌面插件快速上手

本教程在输入框工具栏中添加一个小开关，演示组件本地状态和宿主 UI 注册。它不会改变模型行为，也不会保存设置。需要 Wuu Desktop、`wuu` CLI、Node.js 22 或更高版本，以及匹配的 Wuu 源码检出目录，用于获取 SDK 类型。

## 创建插件包

修改 SDK 源码路径，然后在准备存放插件项目的目录中执行：

```bash
WUU_SOURCE=/absolute/path/to/wuu
npm ci --prefix "$WUU_SOURCE/packages/plugin-sdk"
npm run build --prefix "$WUU_SOURCE/packages/plugin-sdk"
wuu plugin create --type desktop toolbar-demo
cd toolbar-demo
npm pkg set "devDependencies.@wuu/plugin-sdk=file:$WUU_SOURCE/packages/plugin-sdk"
npm install
```

生成的 `plugin.json` 将 `desktop.entry` 指向 `dist/index.js`。桌面模块导出 `activate(api)`，宿主启动对应 generation 时调用它。本例只导入 SDK 类型，因此 TypeScript 会生成没有运行时 import 的独立 ESM 入口。

## 添加开关

将 `src/index.ts` 替换为：

```ts
import type { PluginGenerationApi } from "@wuu/plugin-sdk";

export function activate(api: PluginGenerationApi): void {
  const React = api.react;
  const Toggle = api.ui.ToolbarToggle as unknown as
    (props: Readonly<Record<string, unknown>>) => unknown;

  function DemoToggle() {
    const [enabled, setEnabled] = React.useState(false);
    return React.createElement(Toggle, {
      pressed: enabled,
      "aria-label": "Toggle demo state",
      onClick: () => setEnabled(value => !value),
    }, enabled ? "Demo on" : "Demo off");
  }

  api.registerSlot("composer.toolbar", {
    id: "demo-toggle",
    order: 20,
    render: () => React.createElement(DemoToggle, null),
  });
}
```

`api.react` 是宿主的 React 实例，`api.ui.ToolbarToggle` 提供共享控件，`composer.toolbar` 负责放置，不依赖输入框内部 DOM。通过 API 注册组件，不要额外打包一份 React 运行时。

## 构建和加载

```bash
npm run build
wuu plugin validate .
wuu plugin test .
wuu plugin dev .
```

对于纯桌面插件，`test` 检查包结构，并报告跳过运行时初始化；它不会导入或渲染桌面模块。宿主要求入口自包含；后续添加运行时 import 时，应将依赖打包，而不是让这个文件引用未解析的其他模块。

`dev` 授权传入的目录，并在文件变化时重新构建、发布开发 generation。构建或包检查失败时，保留最后发布的 generation；仍有活动执行时，发布可能等待。激活错误需查看桌面插件状态。

在 Wuu Desktop 中点击开关，确认标签和按下状态变化，再检查键盘焦点、两种主题、窄窗口和较大 UI 字号。禁用插件后，控件应消失。React 状态属于当前挂载的组件，不是持久化的插件存储。

## 打包

```bash
wuu plugin pack .
wuu plugin install ./toolbar-demo-0.1.0.zip
wuu plugin approve toolbar-demo
```

当前 CLI 将暂存包与启用执行的信任决定分开。开发授权保留在本机，不随 zip 分发。桌面插件是受信任的 renderer 代码，不是沙箱中的网页内容。

较大的扩展边界见 [UI 扩展地图](desktop-plugins.md)，动作和视图示例见[插件配方](plugin-recipes.md)，包和生命周期契约见[编写插件](plugin-authoring.md)。
