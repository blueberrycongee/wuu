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

`make setup` 安装桌面、共享客户端核心、保留的 Web/Expo 客户端、插件 SDK、TypeScript 插件、协议包和文档站的锁定依赖，不会安装原生手机工具链或配置远程服务。

`make dev` 运行桌面启动器，在启动 Electron 前构建共享 Web 资源、适用平台的原生辅助程序，以及 TypeScript 插件运行包和当前 Go 核心。应用使用该检出目录的私有 `wuu-core`，不是 `PATH` 中另行安装的 `wuu`。renderer 修改通过 Vite 更新；修改 Go、原生辅助程序或进程启动代码后，应重启启动器，让运行中的进程使用新构建。

桌面安装包在 `Resources/tools` 中内置一份校验过 checksum 的固定版本 ripgrep，并把该目录追加到核心的 `PATH` 末尾，因此即使系统没有安装 `rg`，搜索工具和 agent 的 shell 命令也能使用它；`PATH` 中已有的 `rg` 优先。`make dev` 不会下载它。如需在开发时使用内置版本，先运行一次 `npm --prefix desktop run build:ripgrep`；否则核心使用 `PATH` 中的 `rg`，或回退到内置的搜索实现。

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

八个内置业务插件使用 `plugins/<id>/src/` 中的 TypeScript 源码。
`make build-plugins` 把独立 JavaScript 运行包生成到内嵌资源目录；`make build-go`、
`make install` 和桌面核心构建会先重新生成这些资源，再编译 Go。已提交的运行包
让普通 Go 构建不依赖 npm 安装；`make check-plugins` 会拒绝过期的生成产物。

桌面对所有 Node 插件提供自身的 Electron Node 运行时，安装版无需系统 Node，
也不再包含独立的业务插件辅助程序。CLI 默认使用 `PATH` 中的 Node；可通过
`WUU_NODE_EXECUTABLE` 显式选择运行时。本地 TypeScript 单文件开发需要 Node 22.7
或更新版本，也可以使用桌面运行时。

执行 `make build-plugins` 后，可运行 `./bin/wuu plugin dev plugins/todo`，通过公开的
目录开发流程监听内置插件源码。生成的 `plugin.json` 和 `dist/` 副本不会提交；
清单和界面资源仍编辑 `internal/plugin/bundled/<id>/` 中的原始文件，再重新构建
以更新副本。运行时源码保存后会自动重建。经过授权验证的开发代次优先于已安装包，
同时保留开发来源身份。独立 TypeScript 文件仍可直接运行 `wuu plugin dev ./extension.ts`，
不要求清单或构建。参见[插件快速开始](../customize/plugin-quickstart.md)。

`make install` 从当前检出安装 CLI。当前发布工作流只打包 macOS arm64 桌面预览版；存在 Windows CI 和打包脚本，不代表已经发布 Windows 版本。用户可用的发行方式见[安装](../getting-started/installation.md)。

### 原生引导端到端检查

构建核心后，使用 Node 22.13 或更新版本运行
`WUU_E2E_CORE=bin/wuu node scripts/native-steering-e2e.mjs`。
该脚本启动真实 stdio app-server、受控的原生子进程和临时 SQLite 存储，
检查当前轮次补充、轮次 ID 约束、延迟确认、拒绝、取消、持久化故障、重启恢复
和不支持协议时的排队回退。结果、协议记录和被测二进制哈希保存在
`.tmp/e2e/native-steering/`；可用 `WUU_STEER_E2E_OUTPUT` 指定其他输出目录。
检查只使用合成数据，不调用外部模型，也不验证桌面渲染或已安装的原生引擎。

构建桌面渲染器后，运行 `cd desktop && electron scripts/native-steering-e2e.cjs`，
在正式 UI 中检查恢复出的未确认输入：只读恢复、完整复制、普通待处理项的操作，
以及宽窗口默认字号和窄窗口大字号布局。结果和截图保存在
`desktop/out/e2e/native-steering/`；可用 `WUU_STEER_UI_OUTPUT` 更改目录。

### 实时活动预览验收

安装桌面依赖后，运行 `node desktop/scripts/activity-preview-e2e.cjs`。脚本会打包产品中的 Electron 预览窗口与浏览器/原生内容适配器，打开独立的合成网页，并通过 JSONL 命令与二进制 AVC 会话输出启动合成原生画面宿主子进程。它检查切换提供方时的窗口身份和几何保持、从停靠角落展开的出现动画、切换会话时仅显示所属预览且后台任务继续运行、画面确认、旧回调拒绝、捕获错误呈现、手动隐藏与重新打开、自动预览设置、外部任务控制状态更新以及关闭清理。结果、传输日志、出现动画帧采样与会话可见性结果以及渲染截图保存在 `desktop/out/activity-preview/`；可用 `WUU_ACTIVITY_PREVIEW_OUTPUT` 修改目录。无图形会话的 Linux 自动使用软件渲染和合成显示器。

构建 core 后，运行 `WUU_BROWSER_OWNERSHIP_CORE=bin/wuu node desktop/scripts/browser-task-ownership-e2e.cjs`，检查真实 Go 执行归属和 Chromium 输入。停止必须中断当前任务；之后的新指令应取得新的操作权限，旧执行 ID 仍须被拒绝。该脚本也覆盖浏览器任务隔离、保留页面、待发送输入期间接管与清理。证据保存在 `desktop/out/browser-task-ownership/`。

这些检查不验证 ScreenCaptureKit、原生应用输入、macOS 权限、系统窗口合成或安装包签名。在启用 Computer Use 的 macOS 构建中，还需检查浏览器 → 原生应用 → 浏览器切换、同一应用的两个窗口、将终端和 Wuu 作为普通目标、隐藏或最小化窗口、目标关闭、屏幕录制权限拒绝或撤销，以及待发送输入期间的接管。隐藏和停止不能退出被观察的应用；停用或替换扩展必须停止它的流。还需检查明暗主题、默认与大字号、减少动态效果，以及输入框附近的拖拽和缩放。`node desktop/scripts/inspect-cua-pip.cjs` 可为运行中的原生预览记录进程/窗口诊断与截图；辅助进程正在运行本身不能证明画面实时有效。

## 选择相关检查

| 命令 | 检查或构建内容 |
| --- | --- |
| `make check-go` | 模块一致性、格式、vet，以及 Windows/macOS 交叉构建 |
| `make test-go` | CLI、核心、运行时边界和提示词的 Go 测试 |
| `make check-plugins build-plugins` | TypeScript 插件类型、生成产物一致性及运行包 |
| `make test-plugins` | 在临时宿主环境中验证已打包插件的 JSONL 生命周期、工具、持久化和后台行为 |
| `make check-desktop test-desktop` | 桌面 TypeScript、启动器和单元测试 |
| `make build-desktop` | 共享 Web 资源和 Electron main/preload/renderer 产物，不生成安装包 |
| `make check-clients test-clients build-clients` | 协议/SDK/客户端类型、SDK/客户端测试，以及保留的 Web/Expo 产物 |
| `make test-native` | 桌面 macOS Computer Use 辅助程序测试，不是手机测试 |
| `make build-macos` | 核心/辅助程序、桌面构建和 macOS 目录包 |
| `make docs-policy-check check-docs build-docs` | 文档政策、站点诊断，以及生成站点和链接检查 |

`make check`、`make test`、`make build` 分别汇总对应的仓库目标。`make ci` 运行三者，但不包含原生手机验证、macOS 打包或文档站构建。`make release-check` 包含版本验证、无缓存 Go 测试、桌面测试和 macOS 原生辅助程序检查。发布要求见[发行指南（英文）](../../en/project/release.md)。

类型检查、单元测试和构建成功，与应用实际可用是不同的证据。UI 修改需按[桌面 UI 指南](desktop-ui.md)检查受影响的渲染与交互；插件修改还需验证实际工具调用或界面贡献，包验证不会完成这些检查。

验证 SDK 工具调度时，先执行 `npm --prefix packages/plugin-sdk run build`，再执行 `node scripts/plugin-tool-scheduling-e2e.mjs`。脚本通过真实 JSONL 子进程管道测试官方 TypeScript SDK，以及内置子任务和同级会话工具；宿主服务使用合成响应，不需要构建 Go 或提供模型凭据。结果和协议帧保存在 `.tmp/plugin-tool-scheduling/results.json`；可用 `WUU_E2E_OUTPUT` 指定其他产物目录。它验证 SDK 进程边界，不代表完整 Go 宿主、推理服务或桌面 UI 验收。

`npm --prefix desktop run test:renderer-recovery` 会主动让隐藏的 Chromium 渲染进程
崩溃，检查恢复、重试上限、窗口隔离和 IPC。测试使用临时配置目录和模拟内容，不读取
你的 Wuu 数据。请在图形桌面会话中运行；它不验证原生对话框外观或打包应用行为。

`npm --prefix desktop run test:e2e:stream-settlement` 检查回复完成时段落、标题、
列表、引用和代码的实际渲染位置与尺寸是否稳定，覆盖提前和结束时才确认为最终回答的
两条路径，以及浅色/深色、宽/窄窗口和 14/15/20px 字号。脚本自行启动测试页面服务器
和临时 Electron 配置目录，无需运行中的桌面或模型服务。Linux CI 使用
`xvfb-run --auto-servernum` 并追加 `-- --no-sandbox`。
测量结果和截图保存在 `desktop/out/stream-settlement/`，可用 `WUU_SETTLEMENT_OUTPUT`
更改目录。桌面 CI 运行同一检查并上传证据。

`npm --prefix desktop run test:e2e:stream-code-cursor` 检查代码块位于流式回复末尾时
光标的绘制位置，覆盖未闭合和已闭合的围栏、有无语言标记、空代码块以及列表内的代码块。
光标必须紧跟最后一个代码字符并留在代码卡片内，不能进入被复制的 `<code>` 文本，回复完成
后代码卡片高度保持不变。脚本使用同样的自启动测试页面服务器，覆盖浅色/深色、1000/420px
窗口和 15/20px 字号。测量结果和截图保存在 `desktop/out/stream-code-cursor/`，可用
`WUU_CODE_CURSOR_OUTPUT` 更改目录。

### MCP 连接生命周期

构建核心后，运行 loopback 生命周期验收：

```sh
go build -o bin/wuu ./cmd/wuu
node scripts/mcp-connection-generations-e2e.mjs
```

此脚本使用独立临时数据、合成 MCP 服务和真实 app-server 进程，通过确定性的响应屏障，
验证后台初始化及工具发现期间的 Disconnect、Connect、Refresh 与退出，以及之后的
显式重连和其他服务不受影响。它还会占满四个后台连接名额，验证后续 Disconnect、
Connect 和 Refresh 能覆盖尚在排队的启动任务。它不使用真实凭据，也不验证桌面界面。
`WUU_E2E_CORE` 可指定其他核心二进制；`WUU_MCP_E2E_OUTPUT` 可指定证据目录，
默认为 `.tmp/e2e/mcp-connection-generations/`，保存 JSON RPC 往返、服务端请求、
二进制哈希与通过/失败记录。现有 manager 测试另行覆盖串行 app-server RPC 分发器
无法同时发起的并发操作、永久撤销，以及旧客户端的延迟回调。

### MCP 重载期间的身份验证

运行 `go run ./scripts/mcp-auth-reload-e2e`，使用回环 OAuth/MCP 服务验证共享
运行时与 app-server 协议。夹具将合成凭据保存到临时文件存储，覆盖带凭据启动、
管理器代际替换、跨替换完成登录、旧代前台工具调用、禁用后重新启用，以及无 MCP
启动后首次添加服务器。它还检查管理器和传输会话保持代际独立，RPC 响应不泄露凭据。
夹具不会调用模型或外部引擎。

此夹具通过运行时显式注入 OAuth 服务，不验证 macOS Keychain 或桌面 UI。
结果、连接标识与 RPC 记录默认写入 `.tmp/e2e/mcp-auth-reload/`；可用
`WUU_MCP_AUTH_E2E_OUTPUT` 指定其他证据目录。

### 工作区 PDF 预览

在图形会话中运行 `npm --prefix desktop run test:e2e:workspace-preview`，
用生成的 PDF 验证真实 renderer 和 PDF.js。检查包括重试、命名与显式目标、
键盘和中键跳转、页码范围、来源替换、保留挂载的隐藏面板，以及关闭或重试期间的竞态。外链 IPC 仅记录，
不打开系统浏览器；部分真实目标解析结果会被延迟，以检查过期结果。
JSON、合成 PDF 和截图写入 `desktop/out/e2e/workspace-preview/`，
不使用已安装应用的配置目录或 Go 核心。

无图形会话时，运行
`node desktop/scripts/workspace-preview-e2e.cjs --pdf-fixtures-only`，
用真实 Node PDF.js 验证相同的 PDF 字节。此模式检查解析、命名和页面引用解析，
以及不安全动作数据，不验证渲染或点击。

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

[主 CI 工作流](../../../.github/workflows/ci.yml)按完整 PR 差异选择任务，包含删除文件和重命名前后的路径。PR 始终执行轻量仓库元数据检查。纯文档或原生手机改动跳过主 CI 的产品测试套件；原生手机仍由独立工作流验证。

Renderer 改动运行 Desktop 和 Clients，因为 Web 客户端直接引用 renderer 源码。其他桌面改动运行 Desktop、Clients 和原生系统检查。共享协议、客户端 core、Go、插件及 SDK、上述限定目录之外的依赖锁文件、构建脚本及未知路径保守地运行全部套件。prompts 或插件中的运行时 Markdown 不属于豁免项。Go CI 提供 PostgreSQL，覆盖数据库相关行为。

桌面 CI 在并行单元测试前串行安装 Electron 二进制，避免并发解压同一目录。macOS 和 Windows 在相关 PR 上保留平台专属测试；macOS 在原生资源或打包输入变化时还验证打包。

`main` 推送不再重复 Go、Desktop、Clients 或原生测试套件，只在改动路径选中原生检查时运行 macOS／Windows 打包。手动 `workflow_dispatch` 执行全部检查及两平台打包。因此直接推送 main 不具备 PR 测试覆盖；需要完整验证时应使用 PR 或手动运行 CI。

`Go check` 仅在路径选择明确排除 Go 时接受 Go 任务跳过。`CI check` 要求路径选择成功、所有选中任务成功；意外跳过、失败或取消都会导致汇总失败。这些汇总不修改仓库保护规则。

[文档工作流](../../../.github/workflows/docs.yml)在文档、站点、landing 或相关构建文件变化时检查政策并构建站点。拉取请求只构建，不部署；`main` 构建会部署到 GitHub Pages。产品发行标签使用独立工作流，不发布独立 CLI 压缩包。

## 代码边界

`cmd/wuu` 和 `internal` 包含 CLI 与 Go 核心；`desktop` 负责 Electron main/preload、renderer UI、IPC 和打包。`packages/protocol` 保存共享客户端协议类型，`clients/core` 实现无 UI 的远程行为。官方 TypeScript 插件 SDK 位于 `packages/plugin-sdk`，内置实现位于 `plugins`。`internal/sdk` 是 Go 宿主的内部应用嵌入接口，不是插件编写 API。其他语言的运行时也可实现相同的、与语言无关的 JSONL 进程契约。

Electron API 应留在桌面外壳。新外壳应通过[协议（英文）](../../en/integrations/app-server-protocol.md)与 `wuu app-server` 通信，不应依赖桌面内部实现或另建一套核心。行为变化时同步更新公开文档的中英文版本，放置和检查规则见[文档维护（英文）](../../../docs/README.md)。


## 原生历史协议端到端验证

先运行 `make build-go` 构建 CLI，再运行
`go build -o bin/fakecodex ./internal/codexengine/testdata/fakecodex` 构建本地协议
替身，最后运行 `node scripts/native-history-e2e.mjs`。脚本启动真实 Wuu 与
fake Codex 子进程，并使用临时存储，不使用供应商凭据或真实推理。
覆盖原生分支、编辑截止点、事务失败、不支持的响应、旧记录标识、父子隔离
与重启恢复。结果和原始协议日志保存在 `.tmp/e2e/native-history/`，可通过
`WUU_HISTORY_E2E_OUTPUT` 更改目录。这验证应用服务器协议，不代表桌面渲染
或真实供应商 CLI 已通过验收。

### 真实会话样本验收

运行 `npm --prefix desktop run test:e2e:conversation-corpus`，构建当前 checkout，
通过现有 UI tour 回放十二份经过脱敏的本机会话片段。macOS 使用当前 checkout 的
Wuu Dev host，复用已有签名身份（默认 `Wuu Dev Signing`，也可设置
`WUU_DEV_SIGN_ID`）。若没有可用身份，可设 `WUU_DEV_SIGN_ID=-` 使用临时签名。
验收流程不会创建签名密钥、导入证书或修改钥匙串信任。Linux 需要图形会话或 `xvfb-run --auto-servernum`；CI 传入
`-- --no-sandbox`。`npm --prefix desktop run test:corpus` 检查样本包的共享边界。

样本包位于 `desktop/scripts/fixtures/conversation-corpus/corpus.json`。
保留原话、Markdown、空白、回复顺序和工具调用数量，只针对身份名称、私有服务、
凭据片段、路径和历史定位信息做一致替换。包内不含原始会话 ID、服务配置、隐藏推理、
工具参数与结果、附件。回放中的工具只读取隔离环境内的合成文件。
模型标签来自原始记录；所有回放使用同一个本地脚本服务，因此不验证厂商协议或当前模型行为。

运行器在目标回复输出到一半、完整输出和最终回答完成前暂停，采集明暗主题、
15px 宽窗口和 20px 窄窗口，再采集完成态并重新载入应用，验证持久化历史。
原始数据没有逐字时间记录，分块和节奏明确标记为模拟。
每个检查点通过协议条目 ID 和完整源文本定位当前回复步骤，再滚动目标文字到可见区域，
检查裁剪后的实际几何位置。只有包含工具调用的样本才展开过程历史。
页面横向溢出、内容缺失或不可见、历史缺失、渲染器错误会使运行失败；截图和布局数据用于人工视觉复核，
不代表自动审美评分。

每次运行把截图和 `corpus-report.json` 写到 `artifacts/conversation-corpus/` 下。
`WUU_TOUR_OUTPUT` 指定输出目录；`WUU_TOUR_MATRIX` 可覆盖外观组合，例如
`light:15:1280,dark:20:720`。`WUU_TOUR_ONLY=sample-04` 可单独复现一份样本，
报告会记录所选 ID。`WUU_CORPUS_TEXT_ONLY=1` 只将所选样本的最终回答作为单步、
无工具的对话回放，报告明确标记为 `final-answer-only`，不视作原始回复序列。
CI 额外使用 sample-02 运行此回归，两次运行一起上传为 `conversation-corpus-evidence`。`WUU_CORPUS_SKIP_BUILD=1` 复用已有构建，
`WUU_TOUR_CORE` 指定核心二进制；仅在确认它们对应待验收 checkout 时使用。

新增样本时，先在本地阅读完整片段，一致替换敏感内容，并比较 Markdown 结构、换行数量和
长度变化。原始数据和替换映射保存在公开仓库之外。校验器会拒绝额外字段、常见密钥和
非示例 HTTP 地址，但不能证明任意正文已经脱敏，每份新样本仍须人工复核。
样本中的技术结论属于历史会话内容，不应作为当前产品文档。运行无需真实账号或模型密钥。
