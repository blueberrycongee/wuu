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

## 原生手机与远程服务

当前手机实现位于 [`clients/native`](../../../clients/native/README.md)，iOS 使用 SwiftUI，Android 使用 Jetpack Compose。专用验证命令为：

```bash
bash clients/native/verify.sh all
```

可用 `ios` 或 `android` 选择单个平台。脚本启动隔离的 PostgreSQL 测试环境，构建测试宿主，并运行平台测试和构建。PostgreSQL、Xcode、Java 和 Android SDK 要求见原生 README。通过这些检查不代表完成真机或发布验收。

原生构建打包已提交的头像与过程摘要资源快照，不要求快照与最新桌面源码一致。采用桌面表现层变化属于主动的原生端更新；生成、校验和视觉验收步骤见[共享渲染器说明（英文）](../../../clients/native/shared-ui/README.md)。

旧的 `clients/mobile`、`clients/mobile-web`、`clients/mobile-app` 手机实现已停止开发。部分代码仍参与共享 Web 构建和仓库检查，通过这些检查不能证明原生 App 已通过验证。账号和 relay 部署与本地桌面设置分开，见[远程访问](../automation/remote.md)。

## CI 覆盖范围

[Native mobile](../../../.github/workflows/native-mobile.yml) 仅通过 `workflow_dispatch` 手动运行，不在拉取请求或推送时触发。它保留核心集成检查、iOS/Android 未签名 Release 构建和 Android lint。当前手机端不在发布范围内，纳入发布范围前应恢复自动验证；桌面交付不以移动端验证为前提。

[主 CI 工作流](../../../.github/workflows/ci.yml)运行仓库元数据、Go 检查与测试、桌面检查/测试/构建，以及 SDK/客户端检查/测试/构建。只修改 `docs/` 和 `docs-site/` 时跳过该工作流。Go CI 提供 PostgreSQL，以覆盖依赖数据库的测试。

macOS 拉取请求测试原生辅助程序，推送到 `main` 时还生成桌面目录包。Windows 运行选定的原生进程/沙箱测试和桌面类型检查，在 `main` 上增加未封装打包。完整桌面单元测试在 Ubuntu 运行。这些任务覆盖不同边界，不是在每个系统上重复同一套完整测试。

[文档工作流](../../../.github/workflows/docs.yml)在文档、站点、landing 或相关构建文件变化时检查政策并构建站点。拉取请求只构建，不部署；`main` 构建会部署到 GitHub Pages。产品发行标签使用独立工作流，不发布独立 CLI 压缩包。

## 代码边界

`cmd/wuu` 和 `internal` 包含 CLI 与 Go 核心；`desktop` 负责 Electron main/preload、renderer UI、IPC 和打包。`packages/protocol` 保存共享客户端协议类型，`clients/core` 实现无 UI 的远程行为。插件 SDK 和内置实现位于 `packages/plugin-sdk`、`packages/plugin-go` 和 `plugins`。

Electron API 应留在桌面外壳。新外壳应通过[协议（英文）](../../en/integrations/app-server-protocol.md)与 `wuu app-server` 通信，不应依赖桌面内部实现或另建一套核心。行为变化时同步更新公开文档的中英文版本，放置和检查规则见[文档维护（英文）](../../../docs/README.md)。
