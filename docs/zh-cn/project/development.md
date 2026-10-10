# 开发

从源码检出运行 Wuu，可以修改 Go 核心、Electron 桌面、插件或客户端。以下命令均在仓库根目录执行，贡献与评审规则见[贡献指南（英文）](../../../CONTRIBUTING.md)。

## 准备开发环境

Go 版本以 [`go.mod`](../../../go.mod) 为准，Node 使用 [`.node-version`](../../../.node-version) 指定版本，并安装 npm。桌面包要求 Node 22 或更新版本；macOS 打包和原生 Computer Use 辅助程序还需要 macOS 与 Xcode/Swift 工具链。

```bash
make setup
make dev
```

开发模式使用当前检出目录中的 `.wuu-dev/` 作为 Wuu 数据目录，将配置和运行数据与安装版及其他检出目录隔离。
需要使用已有数据进行 dogfood 时，显式指定绝对路径 `WUU_HOME`，例如
`WUU_HOME="$HOME/.wuu" make dev`。共享数据意味着开发版可以修改安装版的数据，旧版可能无法识别新增配置。
macOS LaunchServices 启动器也会转发该设置。

`make setup` 安装桌面、共享客户端核心、保留的 Web/Expo 客户端、插件 SDK、协议包和文档站的锁定依赖，不会安装原生手机工具链或配置远程服务。

`make dev` 运行桌面启动器，在启动 Electron 前构建共享 Web 资源、适用平台的原生辅助程序，以及当前 Go 核心和插件辅助程序。应用使用该检出目录的私有 `wuu-core`，不是 `PATH` 中另行安装的 `wuu`。renderer 修改通过 Vite 更新；修改 Go、原生辅助程序或进程启动代码后，应重启启动器，让运行中的进程使用新构建。

Project Agent 尚属实验功能，默认构建和发布构建均关闭，没有可供用户开启的设置。
已有项目会话仍可阅读，但不能运行，也不会恢复队列中的任务。开发此功能时，运行
`npm --prefix desktop run dev -- --project-agent`，或使用
`go build -tags project_agent -o bin/wuu ./cmd/wuu` 构建 CLI。
`go test -tags project_agent ./internal/appserver` 验证开启后的行为；普通 Go 测试
验证发布构建的关闭行为。打包发布时必须省略此构建标签。

`npm --prefix desktop run test:e2e:project-agent` 验证后端能力字段缺省、关闭和开启时
的界面，将截图与结果保存到 `desktop/out/e2e/project-agent-gate/`。

只开发 CLI 时可以运行：

```bash
make build-go
./bin/wuu --help
```

`make install` 从当前检出安装 CLI。当前发布工作流只打包 macOS arm64 桌面预览版；存在 Windows CI 和打包脚本，不代表已经发布 Windows 版本。用户可用的发行方式见[安装](../getting-started/installation.md)。

## 选择相关检查

| 命令 | 检查或构建内容 |
| --- | --- |
| `make check-go` | 模块一致性、格式、vet，以及 Windows/macOS 交叉构建 |
| `make test-go` | CLI、核心、Go 插件 SDK、内置插件和提示词的 Go 测试 |
| `make check-desktop test-desktop` | 桌面 TypeScript、启动器和单元测试 |
| `make build-desktop` | 共享 Web 资源和 Electron main/preload/renderer 产物，不生成安装包 |
| `make check-clients test-clients build-clients` | 协议/SDK/客户端类型、SDK/客户端测试，以及保留的 Web/Expo 产物 |
| `make test-native` | 桌面 macOS Computer Use 辅助程序测试，不是手机测试 |
| `make build-macos` | 核心/辅助程序、桌面构建和 macOS 目录包 |
| `make docs-policy-check check-docs build-docs` | 文档政策、站点诊断，以及生成站点和链接检查 |

`make check`、`make test`、`make build` 分别汇总对应的仓库目标。`make ci` 运行三者，但不包含原生手机验证、macOS 打包或文档站构建。`make release-check` 包含版本验证、无缓存 Go 测试、桌面测试和 macOS 原生辅助程序检查。发布要求见[发行指南（英文）](../../en/project/release.md)。

类型检查、单元测试和构建成功，与应用实际可用是不同的证据。UI 修改需按[桌面 UI 指南](desktop-ui.md)检查受影响的渲染与交互；插件修改还需验证实际工具调用或界面贡献，包验证不会完成这些检查。

`npm --prefix desktop run test:renderer-recovery` 会主动让隐藏的 Chromium 渲染进程
崩溃，检查恢复、重试上限、窗口隔离和 IPC。测试使用临时配置目录和模拟内容，不读取
你的 Wuu 数据。请在图形桌面会话中运行；它不验证原生对话框外观或打包应用行为。

### 实时缩放布局护栏

桌面生产构建完成后，在图形会话中运行
`npm --prefix desktop run test:e2e:resize-live-layout`。CI 可以用
`xvfb-run --auto-servernum` 直接调用 Electron 脚本，不需要再次构建 renderer。
测试复用 `resize-e2e-preload.cjs` 的长 Markdown 消息流，注入合成通知并记录浏览器
宿主边界；它不启动真实 Go 进程或嵌入式浏览器。

`desktop/out/e2e/resize-live-layout.json` 记录公开主题/语法 token、宿主几何尺寸，
以及左右拖动和收起/展开动画中的中间消息宽度与文字行数。测试还检查流式跟随、
暂停阅读的终点锚定，以及重新排版期间的提交问题定位和保持。
连续样本用于证明拖动中真实换行；终点检查本身不能证明每个绘制帧的滚动偏移都不变。
CI 不使用实际耗时性能门槛。

在相同环境对源码构建的 A/B 版本做对比时，先运行基线，再运行候选。
用 `WUU_E2E_RENDERER` 分别选择构建后的 `out/renderer/index.html`，用
`WUU_RESIZE_OUTPUT` 保存独立 JSON。候选运行设置 `WUU_RESIZE_COMPARE` 为基线
JSON 路径，就会比较全部公开 token 和稳定后的宿主几何尺寸。此 driver 不注入 CSS。
不要把这类带布局探针的检查与低探针耗时样本混合。
Linux 无界面 Electron 需要
`--ozone-platform=headless --ozone-override-screen-size=1440,1000`，
否则默认显示尺寸可能太小，无法形成有效的桌面视口。

低探针面板诊断使用同一命令和产物选择参数，增加 `WUU_RESIZE_DIAGNOSTIC=panels`。
它在空输入框场景中，分别以相同的 81 个输入步骤往返拖动左右侧栏，复用真实 resize
处理器。JSON 包含 CDP style/layout 计数、动作耗时、rAF 间隔和观察到的 inline
宽度变化；不逐帧读取几何尺寸、不注入 CSS、不做 CPU profile、不录屏，也不设置耗时门槛。
在相同显示条件下顺序运行基线与候选，分别保存结果。此诊断模式独立于完整正确性门禁。

### 会话切换性能护栏

安装依赖后构建核心与桌面，在图形桌面会话中运行真实 Electron/main/preload/Go 测试：

```sh
mkdir -p .tmp/performance
go build -o "$PWD/.tmp/performance/wuu-core" ./cmd/wuu
(cd desktop && ./node_modules/.bin/electron-vite build)
WUU_DESKTOP_CORE="$PWD/.tmp/performance/wuu-core" WUU_SWITCH_CHECK_BUDGET=1 desktop/node_modules/.bin/electron desktop/scripts/session-switch-e2e.cjs
```

测试打开临时合成会话，不发送推理请求。它记录原生点击到内容帧、草稿回显及发送就绪帧
的耗时，再累计到后台恢复完成后两个动画帧的工作量。动画帧不证明物理呈现完成；交互
确认包括输入探针的 IPC 开销。首次访问、重复访问、进程池淘汰与归档阻塞分组记录。
墙钟 P50/P75 仅供诊断，不作为 CI 门槛。

CI 检查大历史样本的重复切换，恢复调用、布局及样式重算的上限由
[`session-switch-budget.json`](../../../desktop/scripts/session-switch-budget.json)
定义。计数包括观察器和输入探针的开销。护栏拒绝提高上限、删除计数项或改变工作负载；
原始结果、报告、日志和最终合成截图以 `session-switch-evidence` 工件上传。

诊断时去掉 `WUU_SWITCH_CHECK_BUDGET`，可设置 `WUU_SWITCH_TURNS=3000`、
`WUU_SWITCH_ROUNDS` 或 `WUU_SWITCH_INIT_DELAY_MS=600`。
`WUU_SWITCH_TRACE=1` 记录带动作、内容及交互标记的 Chromium trace；不要将 trace 运行
与墙钟基线混合统计。`WUU_SWITCH_OUTPUT` 指定证据目录，不包含临时 profile 和数据库。
`WUU_SWITCH_MAIN` 可选择另一个已构建的 main bundle 及相邻 preload/renderer 进行 A/B
验证。结果记录实际加载工件的哈希；仅凭 checkout 提交不能确定外部构建的版本。

诊断订阅历史对导航的影响时，设置 `WUU_SWITCH_SUBSCRIPTION_TURNS=30000`，并去掉
`WUU_SWITCH_CHECK_BUDGET`。夹具会增加约 2 GiB 的临时历史和两个订阅服务，隔离凭据，
禁用外部引擎。它在 IPC 层挂起按需统计响应、随后注入 IPC 失败，检查跨项目新建对话，
并核对真实后端的请求归属和 token 总量。`results.json` 单独记录启动到会话画面的耗时，包含 `subscriptionResults`、
数据库大小、观察到的进程启动次数、导航 RPC 耗时和挂起前的统计响应耗时。
这些是 RPC 整体耗时，不是独立 SQL 计时；首次进入项目不一定启动新进程，应与冷启动区分。
夹具还保存 `subscription-navigation.png`。挂起响应验证导航独立性，不模拟数据库锁或账户服务故障。

### 端到端用户流程诊断

同一测试现在始终记录从主进程 bundle 导入到恢复会话、原生输入草稿、
发送按钮可用且经过两帧的启动耗时。它不包含 Electron 可执行文件启动
或合成数据准备；使用新配置目录，但不清空文件系统缓存。

设置 `WUU_SWITCH_PACED_STREAM=1`，通过真实输入框、preload、主进程及 Go
核心向本地合成 SSE 服务发送请求。服务每 16 ms 按绝对时间发送 256 字节，
内容含 Markdown 段落、代码和表格。测试验证首尾文本进入可见区域、
完整内容持久化，以及流式期间输入的未发送草稿不丢失。
`paced-stream-results.json` 保留逐帧间隔、服务写入时间、渲染事件时间、
长任务、DOM 变更、CDP 工作量/堆内存和 Electron 进程 CPU/内存快照。
主进程和渲染器的时钟域不同，仅比较各自的时间间隔。进程快照不含 Go
核心；未强制 GC 的堆大小不代表分配量，也不能证明内存泄漏。

聚焦归因时可加 `WUU_SWITCH_STREAM_ONLY=1`：保留启动，原生打开长会话 1、5，
再验证快照协议并发送流式请求，记录的缓存面板应与完整流程的 0/1/5 一致。
它省略其他切换和缓存换出场景，结果应单独分析。
`WUU_SWITCH_BUILD_COMMIT` 标识 UI 构建，`WUU_SWITCH_CORE_BUILD_COMMIT`
标识独立选择的核心，便于 UI/核心交叉比较；始终保留实际构建哈希。
仓库外保存的构建必须提供 UI 提交，其源码修改状态记为 null，并应同时保留已验证构建清单。
隔离运行器导出不带 `.git` 的源码时，还需通过 `WUU_SWITCH_SOURCE_COMMIT` 和
`WUU_SWITCH_SOURCE_CHANGES` 标识测试脚本来源，同时保留导出的源码及其哈希清单。
未提供源码修改信息时记为 null，不视为干净的工作区。

输入仍固定在服务已发送约三分之一内容时触发，新增零起始块索引、发送/接收字符进度、
渲染文字大小、渲染器输入/帧时间偏移、主进程派发到帧的耗时和重叠长任务。
这有助于区分事件处理、IPC 调度及流式阶段，不改变触发点追求更好的数字。
`WUU_SWITCH_CPU_PROFILE=1` 和 `WUU_SWITCH_TRACE=1` 现在覆盖流式阶段，
结果标记 `profileOnly`，须与普通计时样本分开。

可选 `WUU_SWITCH_SIDEBAR_THREADS=1500`（额外 30 个项目）或 `5000`（额外 50 个项目）
加入仅含元数据的侧栏历史，覆盖置顶、归档、草稿和依赖 cwd 的旧式会话。
它不模拟真实运行中的进程，不能与 `WUU_SWITCH_CHECK_BUDGET=1` 混用。

基线与候选使用相同最终测试脚本、机器、窗口、依赖和数据量，通过
`WUU_SWITCH_MAIN` 和 `WUU_DESKTOP_CORE` 选择各自匹配的完整构建。至少交错
运行三组，保留原始数据、日志和构建哈希，分开首次打开、重复切换及采样运行。
预热样本应单独保留，不并入测量结果。
无图形 Linux 可用 `--no-sandbox --ozone-platform=headless`；两帧仅表示绘制
机会，不证明物理显示或 120 Hz。墙钟耗时仅供诊断，不作为 CI 阈值。

### 新进程启动诊断

可选的 `startup-e2e.cjs` 驱动在创建 Electron 子进程前启动单调时钟。
先用 `WUU_STARTUP_PREPARE_ONLY=1`、指向新目录的 `WUU_STARTUP_PREPARE_DIR`
以及通常的 turn/sidebar 数量准备一次性数据。准备阶段复用切换 fixture，
将所有运行时记录固定为 `permission_mode=standard`，默认选择会话 1，
并写入合成 fixture 标记，不导入桌面应用。每次试验都用同一 seed core
和数量单独准备新 fixture；不要传入真实 WUU home 或复用 Electron profile。

```bash
# 依赖、生产包和内置 helper 必须已构建。
WUU_STARTUP_PREPARE_ONLY=1 WUU_STARTUP_PREPARE_DIR="$PWD/.tmp/startup-a" \
  WUU_SWITCH_TURNS=3000 WUU_SWITCH_SIDEBAR_THREADS=1500 \
  WUU_DESKTOP_CORE="$SEED_CORE" desktop/node_modules/.bin/electron \
  --no-sandbox --ozone-platform=headless desktop/scripts/session-switch-e2e.cjs
WUU_SWITCH_MAIN="$MAIN_BUNDLE" WUU_DESKTOP_CORE="$TEST_CORE" \
  WUU_SWITCH_BUILD_COMMIT="$UI_COMMIT" WUU_SWITCH_CORE_BUILD_COMMIT="$CORE_COMMIT" \
  node desktop/scripts/startup-e2e.cjs .tmp/startup-a .tmp/startup-a-results
```

测量进程默认启用正常的内置扩展。使用保留构建时，提供已核验的 helper
路径和 source root，并保留 helper 清单；结果记录 core 实际环境、初始化
扩展清单及显式 helper 文件哈希。安全模式是单独标记的对照。这是已配置的
返回用户 fixture，不是全新安装的 onboarding 测量，也不使用真实凭据、
账号或推理服务。

Linux 驱动在创建窗口前配置 1440×1000 的无头显示器。产品自行决定初始
窗口尺寸；harness 不会在导航后调整大小或手动显示窗口。零 viewport
按无效测试环境失败。结果保留显示器、工作区、缩放、可见性及合成状态。
终点要求目标历史的最后标记已出现、原生可信输入进入可见可编辑的 composer，
并且输入值和可用的 Send 按钮持续两个动画帧。不据此声称物理呈现或 OS 冷启动。

`startup-results.json` 区分父进程 spawn、窗口/导航、前后台 core 请求、
main IPC、历史 DOM 及输入就绪。core 响应与 main IPC 耗时重叠，不能相加。
同步偏好 IPC 只覆盖 main handler，不含 renderer 往返。回溯读取的
PaintTiming 保留 renderer 导航时钟；CDP 计数从 debugger 连接后开始，
更早的解析/执行和首次 React commit 不在覆盖范围。子进程枚举失败时
helper CPU/IO 不可用。不要混合新进程、普通切换、流式、profile 或旧诊断
harness 数据。隔离 core 优化时使用完全相同的 UI，保留全部重复样本，
并以最终 main 修订作为优化基线。

## 原生手机与远程服务

当前手机实现位于 [`clients/native`](../../../clients/native/README.md)，iOS 使用 SwiftUI，Android 使用 Jetpack Compose。专用验证命令为：

```bash
bash clients/native/verify.sh all
```

可用 `ios` 或 `android` 选择单个平台。脚本启动隔离的 PostgreSQL 测试环境，构建测试宿主，并运行平台测试和构建。PostgreSQL、Xcode、Java 和 Android SDK 要求见原生 README。通过这些检查不代表完成真机或发布验收。

iOS 构建打包已提交的小球与过程摘要资源快照，不要求快照与最新桌面源码一致。采用桌面表现层变化属于主动的 iOS 更新；生成、校验和视觉验收步骤见[共享渲染器说明（英文）](../../../clients/native/shared-ui/README.md)。

旧的 `clients/mobile`、`clients/mobile-web`、`clients/mobile-app` 手机实现已停止开发。部分代码仍参与共享 Web 构建和仓库检查，通过这些检查不能证明原生 App 已通过验证。账号和 relay 部署与本地桌面设置分开，见[远程访问](../automation/remote.md)。

## CI 覆盖范围

[Native mobile](../../../.github/workflows/native-mobile.yml) 在修改 iOS 客户端、共享原生测试环境、远程协议或 Go 依赖的拉取请求与 `main` 推送上，运行 iOS 核心集成和未签名模拟器／真机目标构建。手动 `workflow_dispatch` 还会运行原有 Android 集成、Release 构建和 lint。这些检查不覆盖真机签名、商店分发或实体手机交互。

[主 CI 工作流](../../../.github/workflows/ci.yml)运行仓库元数据、Go 检查与测试、桌面检查/测试/构建，以及 SDK/客户端检查/测试/构建。只修改 `docs/` 和 `docs-site/` 时跳过该工作流。Go CI 提供 PostgreSQL，以覆盖依赖数据库的测试。

桌面 CI 在并行单元测试前先串行安装一次 Electron 二进制。Electron 包会在首次导入时下载，不能让多个测试进程同时向同一安装目录解压。

`Go check` 汇总要求两个 Go 任务都成功。拉取请求中始终执行汇总，依赖任务失败、跳过或取消都不能通过该门禁。
只有合并后的 `push` 运行在整个工作流取消时才跳过或取消汇总；如果 Go 依赖已有真实失败，汇总仍然失败。
GitHub 允许已跳过的必需检查放行合并，因此这项例外不适用于拉取请求，也不修改仓库保护规则。

macOS 拉取请求测试原生辅助程序，推送到 `main` 时还生成桌面目录包。Windows 运行选定的原生进程/沙箱测试和桌面类型检查，在 `main` 上增加未封装打包。完整桌面单元测试在 Ubuntu 运行。这些任务覆盖不同边界，不是在每个系统上重复同一套完整测试。

[文档工作流](../../../.github/workflows/docs.yml)在文档、站点、landing 或相关构建文件变化时检查政策并构建站点。拉取请求只构建，不部署；`main` 构建会部署到 GitHub Pages。产品发行标签使用独立工作流，不发布独立 CLI 压缩包。

## 代码边界

`cmd/wuu` 和 `internal` 包含 CLI 与 Go 核心；`desktop` 负责 Electron main/preload、renderer UI、IPC 和打包。`packages/protocol` 保存共享客户端协议类型，`clients/core` 实现无 UI 的远程行为。插件 SDK 和内置实现位于 `packages/plugin-sdk`、`packages/plugin-go` 和 `plugins`。

Electron API 应留在桌面外壳。新外壳应通过[协议（英文）](../../en/integrations/app-server-protocol.md)与 `wuu app-server` 通信，不应依赖桌面内部实现或另建一套核心。行为变化时同步更新公开文档的中英文版本，放置和检查规则见[文档维护（英文）](../../../docs/README.md)。
