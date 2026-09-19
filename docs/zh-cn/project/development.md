# 开发

本页说明仓库的构建和检查入口。Agent 执行约束位于 `AGENTS.md`；贡献者不需要
依靠该文件来发现日常开发命令。

## 环境要求

- Go 版本以 `go.mod` 为准。
- Node.js 22 或更新版本，CI 基准由 `.node-version` 指定；使用 npm 安装依赖。
- Electron 的 macOS 安装包及原生 CUA 辅助程序需要 macOS、Xcode 和 Swift。

Go CLI 和核心支持 macOS、Linux。当前桌面发布是 Apple silicon macOS 预览版；
手机与远程控制仍在开发中，没有稳定的手机公开发行版。

## 安装与日常命令

在仓库根目录运行：

| 命令 | 用途 |
| --- | --- |
| `make setup` | 安装桌面、客户端、协议与文档站的锁定 npm 依赖 |
| `make dev` | 启动真实 Electron 开发入口 |
| `make check` | 检查仓库元数据、测试政策、Go 模块/格式/vet 和 TypeScript 类型 |
| `make test` | 运行 Go、桌面、扩展 SDK、远程核心及旧 Web/Expo 客户端测试 |
| `make build` | 构建 Go CLI、Electron renderer/main 及旧 Web/Expo 客户端产物 |
| `make ci` | 运行跨平台检查、测试和构建 |
| `make release-check` | 核对发行版本并运行 Go 核心与桌面测试门禁 |

可以按组件运行：

```bash
make check-go test-go build-go
make check-desktop test-desktop build-desktop
make check-clients test-clients build-clients
make test-native
make build-macos
```

`make test-native` 和 `make build-macos` 需要 macOS。桌面开发启动器从当前 Go 源码
构建并启动 `wuu app-server`。修改 Go 或 Electron 主进程后需完整重启 `make dev`；
它们不支持热重载。

`make test-native` 测试的是桌面 CUA 辅助程序，不是手机 App。当前手机开发位于
[clients/native](../../../clients/native/README.md)，iOS 使用 SwiftUI，Android
使用 Jetpack Compose。运行 `bash clients/native/verify.sh all` 进行隔离集成检查；
PostgreSQL、Xcode、Android 环境要求及未完成的发布验收见该 README。旧 Expo、
WebView、Capacitor 手机路线已于 2026-09-12 停止开发。现有客户端门禁和桌面共享
Web 产物仍消费其中部分代码，但不能据此判断原生 App 已通过验证。

文档修改运行 `make docs-policy-check build-docs`，维护规则见
[文档说明（英文）](../../../docs/README.md)。可复用界面预览和滚动处理见
[桌面 UI 维护](desktop-ui.md)。

## CI 检查

除仅修改文档的情况外，拉取请求及推送到 `main` 会运行：

- **仓库检查：** 版本、评估记录、文档政策、主题契约与测试政策。
- **Go 检查：** 模块一致性、格式、vet、Windows/macOS 交叉构建和测试；`main` 另运行独立 CLI 构建。
- **桌面检查：** 依赖安装、类型检查、单元测试和 Electron 构建。
- **客户端检查：** 协议、扩展 SDK、核心和旧客户端的类型检查、客户端测试及 Web/Expo 构建。
- **macOS 原生检查：** 拉取请求运行 Swift/原生辅助程序测试，`main` 另运行 Electron 目录打包。
- **Windows 原生检查：** 拉取请求检查进程/沙箱边界及桌面类型，`main` 另运行未封装打包；完整桌面单元测试已在 Ubuntu 运行。

发行标签增加持久自签身份的 macOS DMG/ZIP 检查，不使用 Apple Developer ID。GitHub Releases
不发布独立 CLI 压缩包，见 [发行指南（英文）](../../en/project/release.md)。独立文档
工作流在 docs、站点或 landing 变动时检查政策并构建站点，在 `main` 部署。

## 产品边界

- `internal/` 和 `cmd/wuu/` 是可复用 Go 核心与 app-server。
- `desktop/` 是 Electron 外壳，负责原生 UI、IPC 与打包。
- `packages/protocol/` 是共享客户端协议类型来源。
- `clients/core/` 是无 UI 的远程客户端。
- `clients/native/` 是当前原生 iOS 和 Android App。
- `clients/mobile/`、`clients/mobile-web/`、`clients/mobile-app/` 保留旧手机实现，不代表当前手机功能路线。

不要把 Electron API 引入 Go 核心。新外壳应启动 `wuu app-server`，而不是分叉或导入核心。
