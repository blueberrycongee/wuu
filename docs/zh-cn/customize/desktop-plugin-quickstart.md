# 桌面插件快速上手

本教程在输入框工具栏中添加一个小开关，演示组件本地状态和宿主 UI 注册。它不会改变模型行为，也不会保存设置。你可以直接将它作为单个 `.ts` 文件运行或安装（需要 Node.js 22.7+，无需 `plugin.json` 或构建步骤），也可以创建完整的插件包目录。

## 方式 A：直接运行或安装单个 `.ts` 文件

将下方的开关代码保存为 `toolbar-demo.ts`，直接执行：

```bash
wuu plugin dev ./toolbar-demo.ts
# 或直接作为受信任的用户插件安装：
wuu plugin install ./toolbar-demo.ts
```

Wuu 会自动擦除 TypeScript 类型，将 `@wuu/plugin-sdk` 运行时导入内联为自包含的浏览器模块，并自动合成 manifest。如果同一个 `.ts` 文件同时包含 `export default { initialize() ... }`，Wuu 会从这一个文件同时激活后端运行时与桌面 UI。注册视图、presenter、设置项或主题时，也可以按需导出 `export const manifest = { contributes: { ... } }`。

## 方式 B：创建插件包目录

修改 SDK tarball 路径，然后在准备存放插件项目的目录中执行：

```bash
wuu plugin create --type desktop toolbar-demo
cd toolbar-demo
npm install --save-dev /absolute/path/to/wuu-plugin-sdk-0.1.0.tgz
```

生成的 `plugin.json` 将 `desktop.entry` 指向 `dist/index.js`。桌面模块导出 `activate(api)`，宿主启动对应 generation 时调用它。本例只导入 SDK 类型，因此 TypeScript 会生成没有运行时 import 的独立 ESM 入口。

## 添加开关

将 `src/index.ts` 替换为：

```ts
import type { PluginGenerationApi } from "@wuu/plugin-sdk";

export function activate(api: PluginGenerationApi): void {
  const React = api.react;
  const Toggle = api.ui.ToolbarToggle;

  function DemoToggle() {
    const [enabled, setEnabled] = React.useState(false);
    return React.createElement(Toggle, {
      pressed: enabled,
      "aria-label": "Toggle demo state",
      onClick: () => setEnabled(value => !value),
    }, enabled ? "Demo on" : "Demo off");
  }

  api.registerUIContribution({
    id: "demo-toggle",
    title: "Demo toggle",
    target: "wuu.default-composer/composer.toolbar",
    order: 20,
    render: () => React.createElement(DemoToggle, null),
  });
}
```

`api.react` 是宿主的 React 实例，`api.ui.ToolbarToggle` 提供共享控件，`wuu.default-composer/composer.toolbar` 负责放置，不依赖输入框内部 DOM。通过 API 注册组件，不要额外打包一份 React 运行时。

## 构建和加载

```bash
npm run build
wuu plugin validate .
wuu plugin test .
wuu plugin dev .
```

对于纯桌面插件，`test` 检查包结构，并报告跳过运行时初始化；它不会导入或渲染桌面模块。宿主要求入口自包含；后续添加运行时 import 时，应将依赖打包，而不是让这个文件引用未解析的其他模块。

`dev` 授权传入的目录，并在文件变化时重新构建、发布开发 generation。构建或包检查失败时，保留最后发布的 generation；仍有活动执行时，发布可能等待。激活错误需查看桌面插件状态。桌面注册有独立的生命周期。如果再添加原生运行时入口，其提交后激活失败不会回滚包变更，详见[插件架构](plugin-system.md#运行时-generation)。

在 Wuu Desktop 中点击开关，确认标签和按下状态变化，再检查键盘焦点、两种主题、窄窗口和较大 UI 字号。禁用插件后，控件应消失。React 状态属于当前挂载的组件，不是持久化的插件存储。

## 打包

```bash
wuu plugin pack .
wuu plugin install ./toolbar-demo-0.1.0.zip
wuu plugin approve toolbar-demo
```

当前 CLI 将暂存包与启用执行的信任决定分开。开发授权保留在本机，不随 zip 分发。桌面插件是受信任的 renderer 代码，不是沙箱中的网页内容。

较大的扩展边界见 [UI 扩展地图](desktop-plugins.md)，动作和视图示例见[插件配方](plugin-recipes.md)，包和生命周期契约见[编写插件](plugin-authoring.md)。

单文件 TSX 组件、完整 Shell 与嵌套贡献，参见[直接 TSX 预览和公开 UI SDK](ui-sdk.md)。
