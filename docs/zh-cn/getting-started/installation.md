# 安装

桌面应用自带 Wuu core。只有需要从终端或脚本运行 Wuu 时，才需要单独安装 CLI。

## 在 macOS 上安装

当前发布流程构建 Apple 芯片版本。安装前，请查看 [GitHub Releases](https://github.com/blueberrycongee/wuu/releases) 中对应版本的附件和说明。

1. 下载 `wuu-mac-arm64.dmg` 或 `wuu-mac-arm64.zip`。
2. 将 `wuu.app` 移入 `/Applications`，从那里打开。
3. 按[首次设置](index.md)选择执行引擎并连接模型服务。

发布流水线要求 Apple Developer ID 正式签名和公证，并将公证票据附加到 App 和 DMG。请查看下载版本的发布说明：早期预览版仅使用 ad-hoc 签名，无法验证发布者身份。以下处理方法仅适用于这些可信的旧预览版。不要为了绕过提示而全局关闭系统安全保护或安装证书。

如果已移入 `/Applications` 的可信官方安装包仍被阻止，可仅移除该应用的隔离标记，再重新打开：

```bash
xattr -dr com.apple.quarantine /Applications/wuu.app
```

ad-hoc 签名不能证明发布者身份，也不能保证更新后保留 macOS 权限。

下载文件名不再包含版本号；应用和发布说明仍保留版本，自动更新通过版本元数据和压缩包校验值识别更新。旧版本的下载文件名可能仍带版本号。

## 在 Linux 上安装

选择包含 `wuu-linux-x64.AppImage` 和 `wuu-linux-x64.deb` 的发布版本。
之前仅提供 macOS 附件的版本（包括 v2026.10.18）没有 Linux 安装包。
Linux x64 需要 X11 或 XWayland 图形桌面。首批桌面验收使用 Debian 13；
发布检查在 Ubuntu 22.04 上运行解包后的安装包。包内启动器选择 X11；
这些安装包不支持原生 Wayland 或 arm64。

请只从官方 Release 下载。`SHA256SUMS-linux` 用于检查下载字节，不能证明
发布者身份。Linux 包不使用 Apple Developer ID 签名或公证。

Debian 用户下载 `.deb` 后可安装或升级：

```bash
sudo apt install ./wuu-linux-x64.deb
```

从桌面应用菜单打开 Wuu；终端启动使用 `wuu --ozone-platform=x11`。
Debian 包会安装桌面入口、Electron 库和私有 core，无需另装 Node 或 Go。

Ubuntu 24.04 上的安装尚未完成验收。`.deb` 包含针对 `/opt/wuu/wuu` 的
Wuu 专用 AppArmor 配置，允许 Chromium 所需的用户命名空间，但实际安装
路径仍需单独验证。默认配置的 Ubuntu 24.04 不支持 AppImage 或解包版启动。

AppImage 应保存在当前用户可写的目录，然后运行：

```bash
chmod +x wuu-linux-x64.AppImage
./wuu-linux-x64.AppImage
```

AppImage 需要非特权用户命名空间，以及宿主的 GTK 3、NSS、ALSA 库及 FUSE 2（依发行版为 `libfuse2`
或 `libfuse2t64`）。没有 FUSE 时可解包运行，不必修改沙箱设置：

```bash
./wuu-linux-x64.AppImage --appimage-extract
./squashfs-root/AppRun
```

解包目录没有应用内更新，请手动替换。Chromium 沙箱保持开启。如果宿主
阻止沙箱启动，请使用兼容的桌面配置，不要添加 `--no-sandbox` 或关闭系统
安全控制。

只读和标准权限下的工具执行需要内核启用 Landlock ABI 3 或更高版本。
缺少能力时 Wuu 会拒绝受限命令；安装 AppImage 不会补上内核沙箱。
MCP OAuth 还需要会话 D-Bus 和已解锁的 Secret Service 密钥环（例如
GNOME Keyring），不会回退到明文凭据。Linux 不提供原生 macOS Computer Use
或应用快照。

## 更新应用

带有 **Wuu > 检查更新** 菜单的正式签名 macOS 构建会在启动后及每六小时检查 GitHub 稳定版。确认下载后，等工作保存、运行中的任务可以停止时，选择「重启并安装更新」。选择「稍后」会保持应用运行；普通退出不会自动安装已下载的更新。不会推送预发布版或降级版本。

没有该菜单的旧版本需要先手动更新一次：下载新版，用 **Cmd+Q** 退出 Wuu，再替换 `/Applications/wuu.app`。只关闭窗口不等于退出应用。请从「应用程序」打开替换后的版本，不要同时运行下载目录或磁盘映像中的另一份应用。开发和 ad-hoc 构建不会检查更新。

正式发布的 AppImage 提供 **帮助 > 检查更新** 和侧栏更新入口，下载和重启
仍需确认。请将 AppImage 保存在可写目录。Debian 包与解包版 AppImage
需要手动更新：退出 Wuu，再安装下一版 `.deb` 或替换解包目录。完整的
AppImage 跨版本升级需要用两个发布版本单独验收，打包冒烟检查不能替代它。

设置和会话保存在应用包之外。替换应用时保留这些数据，并查看发布说明是否有该版本特有的迁移要求。

## 取决于构建方式的功能

macOS 发布流程包含 Computer Use。手机访问取决于该版本的构建开关，请查看发布说明。账号登录仍仅限开发构建。旧版下载包的功能可能不同，请查看对应发布说明。

Computer Use 已内置所需原生辅助程序，无需单独安装或设置环境变量。截取屏幕和操作桌面需要**屏幕录制**和**辅助功能**权限。

macOS 首次设置会提供这两项权限。可以跳过，之后在**设置 → 常规 → 系统权限**中设置。点击**启用**，将浮动引导中的应用图标拖入系统设置列表，再开启访问权限。也可以点击图标，在 Finder 中找到正在运行的应用后手动添加。Wuu 会自动刷新授权状态。

授权确认由 macOS 处理，系统可能要求重启。更新后 macOS 也可能再次要求授权；请授权实际运行的应用，无需删除 Wuu 数据或重置其他应用的隐私权限。

## 从源码安装 CLI

安装 [go.mod](../../../go.mod) 要求的 Go 版本，以及 Node.js 22.19 或更高版本，
然后运行：

```bash
git clone https://github.com/blueberrycongee/wuu.git
cd wuu
npm ci --prefix plugins
make install
wuu --version
```

`make install` 使用 Go 的安装目录。如果找不到 `wuu` 且没有设置 `GOBIN`，将默认目录加入 shell 的 `PATH`：

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

如果设置了 `GOBIN`，应添加该目录。CLI 与桌面应用内置的 core 独立安装，版本可能不同。发布流程不提供独立 CLI 压缩包。产品标签使用日期版本，请从检出的源码安装，不要使用 `go install ...@latest`。

内置 CLI 引擎现在默认使用隔离的程序化工具调用。升级时请保留 `PATH` 中的
Node，或在用户配置中设置 `ptc.node_executable`。缺少 Node 或版本不支持时，
普通工具将无法执行；如需直调工具，可明确设置 `ptc.enabled` 为 `false`。
原有明确保存的全局与家族选择保持不变。桌面应用自带运行时，无须另装 Node。

接下来[连接模型服务](model-services.md)。构建桌面应用请参阅[开发指南](../../en/project/development.md)（英文）。
