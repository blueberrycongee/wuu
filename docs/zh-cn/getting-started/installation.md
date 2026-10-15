# 安装

桌面应用自带 Wuu core。只有需要从终端或脚本运行 Wuu 时，才需要单独安装 CLI。

## 在 macOS 上安装

当前发布流程构建 Apple 芯片版本。安装前，请查看 [GitHub Releases](https://github.com/blueberrycongee/wuu/releases) 中对应版本的附件和说明。

1. 下载 `wuu-<version>-mac-arm64.dmg` 或 `wuu-<version>-mac-arm64.zip`。
2. 将 `wuu.app` 移入 `/Applications`，从那里打开。
3. 按[首次设置](index.md)选择执行引擎并连接模型服务。

发布流水线要求 Apple Developer ID 正式签名和公证，并将公证票据附加到 App 和 DMG。请查看下载版本的发布说明：早期预览版仅使用 ad-hoc 签名，无法验证发布者身份。以下处理方法仅适用于这些可信的旧预览版。不要为了绕过提示而全局关闭系统安全保护或安装证书。

如果已移入 `/Applications` 的可信官方安装包仍被阻止，可仅移除该应用的隔离标记，再重新打开：

```bash
xattr -dr com.apple.quarantine /Applications/wuu.app
```

ad-hoc 签名不能证明发布者身份，也不能保证更新后保留 macOS 权限。

## 更新应用

带有 **Wuu > 检查更新** 菜单的正式签名 macOS 构建会在启动后及每六小时检查 GitHub 稳定版。确认下载后，等工作保存、运行中的任务可以停止时，选择「重启并安装更新」。选择「稍后」会保持应用运行；普通退出不会自动安装已下载的更新。不会推送预发布版或降级版本。

没有该菜单的旧版本需要先手动更新一次：下载新版，用 **Cmd+Q** 退出 Wuu，再替换 `/Applications/wuu.app`。只关闭窗口不等于退出应用。请从「应用程序」打开替换后的版本，不要同时运行下载目录或磁盘映像中的另一份应用。开发和 ad-hoc 构建不会检查更新。

设置和会话保存在应用包之外。替换应用时保留这些数据，并查看发布说明是否有该版本特有的迁移要求。

## 取决于构建方式的功能

macOS 发布流程包含 Computer Use，以及用于原生 iOS 配对的手机访问功能。账号登录仍仅限开发构建。旧版下载包的功能可能不同，请查看对应发布说明。

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
