# Wuu Plugin

Wuu Plugin 是一个可安装、可升级的扩展包。它可以只提供一种能力，也可以把
Agent runtime、Desktop UI、主题、设置、Skills、Hooks、MCP server 和命令一起交付。
安装插件表示你信任它的代码以你的用户权限执行；Wuu 不对插件做沙箱。

如果你还不确定是否需要插件，先看[扩展 Wuu](index.md)。一个只需要任务说明的需求适合
Skill，一个已有工具服务适合 MCP；需要代码生命周期、宿主服务或桌面 UI 时才需要
Wuu Plugin。

插件平台当前是本地优先的：没有市场或中心仓库，插件作者通常在自己的仓库中开发和发布。
当前安装器只接受本地目录或 zip 包，尚不能直接从 npm 或 Git source 安装。

## 一个插件包可以包含什么

| 类型 | 做什么 | 是否需要代码 |
| --- | --- | --- |
| 声明式贡献 | 主题、设置、Skills、Hooks、MCP servers 和命令 | 视贡献而定 |
| Agent 插件 | 注册工具、贡献上下文、变换请求、观察 Turn、提供或消费服务 | 是，独立进程 |
| Desktop 插件 | 添加 View、Slot、Presenter、Surface、样式和交互卡片 | 是，Renderer 代码 |

同一个包可以同时声明 `runtime` 和 `desktop.entry`。例如，Agent runtime 负责查询私有
服务，Desktop 模块负责展示结果；两部分共享插件 ID 和同一次安装/信任生命周期。

插件管理、安全模式、崩溃恢复和原生窗口生命周期始终由 Wuu 控制，
插件不能替换这些恢复路径。强风格外观插件可以通过公开 Token、UI Kit 和语义锚点统一改变
整个产品，但窗口安全区、导航结构、Tab、滚动、溢出和恢复入口仍由宿主管理。

第一次开发时直接选择一条路径：

- [Agent 插件快速上手](plugin-quickstart.md)：注册模型可见工具并调用宿主 Storage；
- [Desktop 插件快速上手](desktop-plugin-quickstart.md)：在 Composer 加入一个真实按钮；
- [Desktop UI 扩展地图](desktop-plugins.md)：按界面位置选择 View、Slot、Presenter 或 Surface；
- [插件场景教程](plugin-recipes.md)：输入框按钮、选区浮层和完整面板等组合方式。

## 获取与安装

在 Wuu Desktop 的插件目录中选择本地目录或 zip 包。安装后会直接打开该插件详情；点击一次
**批准并启用**就是信任确认，并立即启用插件。

包管理 CLI 当前提供的是较底层的本地包流程：

```bash
wuu plugin install ./foo
wuu plugin install ./foo-1.0.0.zip
```

CLI 会先暂存本地包；使用 `wuu plugin approve <id>` 激活该 fingerprint。这个拆分的 CLI
属于兼容入口，并不代表另一套信任模型。插件文件安装在 `~/.wuu/plugins/`（设置 `WUU_HOME`
时在其下）。无论从哪个入口操作，批准并启用代码都是信任决定：代码以你的用户权限执行。

## 信任延续、更新与用户可见状态

- 批准并启用包表示信任该包的代码；
- 当前本地包更新器会暂存每个新 fingerprint；确认替换前，已安装 generation 继续运行；
- 受信任项目目录中的扩展随项目信任加载，不逐插件确认；
- 传给 `wuu plugin dev` 的路径属于明确的开发执行，不继承已安装包的信任；
- 更新失败时报告失败并保留可恢复入口，不会把用户送回 onboarding。

插件只有三种用户可见状态：`Enabled`（已安装并运行）、`Disabled`（用户明确停用）、
`Failed`（加载或运行失败，可查看错误并禁用）。

```bash
wuu plugin list
wuu plugin disable my-plugin
wuu plugin remove my-plugin
```

## 恢复与故障处理

- **加载失败：**错误在插件列表和设置页可见；禁用该插件或重装即可，其余启用的插件继续运行。
- **界面渲染失败：**Wuu 只回退出错的 Slot、Presenter、Surface 或 View，插件管理和
  默认 UI 恢复入口仍可用。
- **需要立即隔离：**运行 `wuu plugin disable <id>`。即使 Desktop 贡献有问题，也可以
  使用 CLI 禁用插件；若 Wuu 因崩溃进入安全模式，先保持问题插件禁用。
- **不再需要：**运行 `wuu plugin remove <id>`。当前默认保留插件设置和 Storage，不要把
  “移除包”理解为清除全部用户数据。

## 常见能力

- **换主题**：获批且启用的插件主题出现在"设置 → 外观"，禁用或切回内置主题时
  Token 被完整移除。主题只需要 `plugin.json` 声明，不执行代码。用户操作见
  [插件主题与设置](themes-settings.md)。
- **加设置**：插件可以声明自己的设置项（开关、文本、数字、枚举），在设置界面生成
  控件，按插件命名空间存储。禁用、升级或卸载插件时默认保留设置和 Storage，便于之后
  恢复；当前不会隐式删除这些数据。
- **扩展 Agent**：Agent 插件以独立进程运行，可以注册模型可见的工具、贡献系统提示
  与模型步骤前的持久上下文、参与请求改写与摘要压缩，并观察 Turn 生命周期。插件包还可
  通过 manifest 声明独立的命令或模型 Hook；它们不属于 runtime capability，具体事件与
  信任边界见 [Hooks](hooks.md)。
- **定制桌面界面**：注册全局样式、在稳定区域放置可持久化的 View、替换或包装语义
  Presenter（消息、工具活动、导航、设置等）、在固定 Slot 插入内容。渲染失败只回退
  当前边界，设置、禁用和默认 UI 恢复始终可用。
- **组合多种扩展**：插件包可以携带 Skills、Hooks 和 MCP server 定义，让安装、
  更新和卸载使用同一条生命周期。

## 信任边界

- 声明式主题只能修改公开的语义 Token，适合直接安装。
- Agent 插件的 runtime 进程与 Wuu 拥有相同用户权限；桌面插件可以注册任意 CSS。
  这两类只安装你信任的来源。
- Renderer 不读取插件绝对路径，加载前由 app-server 记录来源身份，Electron 主进程
  通过内容寻址的 `wuu-plugin:` 协议加载；CSP 不开放 `unsafe-eval`。
- Wuu 不审核、不认证、也不沙箱插件代码；更新按来源身份延续信任，不按内容逐次审批。
- 插件声明的 Hook 与直接运行第三方本地命令具有相同风险。

## 当前边界

跨具备兼容性的 Wuu 产品版本继续工作的承诺（开发者不 fork 也能跟上更新）是插件平台当前阶段的
完成门槛，但尚未验证完成。在这之前，插件可能随 Wuu 升级而需要调整；发布插件时声明
`minimum_wuu_version` 可以避免不兼容组合被激活。

插件还可以声明简单的包关系：缺少 `requires` 中的插件时不会启动；`breaks` 命中时 Wuu
拒绝同时启用；`conflicts` 只显示潜在冲突提示，不会替你选择或自动停用插件。当前不做
版本范围、SAT 求解或组合评分。

## 开发与发布

CLI 提供完整的本地开发闭环：

```bash
wuu plugin create --type agent my-agent
wuu plugin create --type desktop my-ui
wuu plugin create --type full my-extension

wuu plugin validate ./my-extension
wuu plugin build ./my-extension
wuu plugin test ./my-extension
wuu plugin dev ./my-extension
wuu plugin pack ./my-extension
```

完整 manifest、Agent 协议、Desktop API、生命周期和安全边界见
[插件开发参考](plugin-authoring.md)。底层设计和宿主所有权见
[插件系统架构](plugin-system.md)。
