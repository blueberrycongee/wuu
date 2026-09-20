# 安装

桌面应用自带 Wuu core。只有需要从终端或脚本运行 Wuu 时，才需要单独安装 CLI。

## 在 macOS 上安装

当前发布流程构建 Apple 芯片版本。安装前，请查看 [GitHub Releases](https://github.com/blueberrycongee/wuu/releases) 中对应版本的附件和说明。

1. 下载 `wuu-<version>-mac-arm64.dmg` 或 `wuu-<version>-mac-arm64.zip`。
2. 将 `wuu.app` 移入 `/Applications`，从那里打开。
3. 按[首次设置](index.md)选择执行引擎并连接模型服务。

发布流程使用固定的自签身份，没有采用 Apple Developer ID 签名和公证。如果 macOS 阻止打开，请先确认应用来自官方发布，再到**系统设置 → 隐私与安全性 → 仍要打开**放行。不要为了绕过提示而全局关闭系统安全保护或安装证书。

## 更新应用

下载新版后，用 **Cmd+Q** 退出 Wuu，再替换 `/Applications/wuu.app`。只关闭窗口不等于退出应用。请从「应用程序」打开替换后的版本，不要同时运行下载目录或磁盘映像中的另一份应用。

设置和会话保存在应用包之外。替换应用时保留这些数据，并查看发布说明是否有该版本特有的迁移要求。

## 取决于构建方式的功能

当前公开发布流程关闭了账号、远程控制和 Computer Use 功能。文档中有关这些功能的说明适用于启用了它们的构建；源码中存在某项功能，不代表下载的应用已经包含它。

启用了 Computer Use 的 macOS 构建可能需要**屏幕录制**和**辅助功能**权限，才能截取屏幕或操作桌面。请授权实际运行的应用。更新后 macOS 可能再次要求授权，无需删除 Wuu 数据或重置其他应用的隐私权限。

## 从源码安装 CLI

安装 [go.mod](../../../go.mod) 要求的 Go 版本，然后运行：

```bash
git clone https://github.com/blueberrycongee/wuu.git
cd wuu
make install
wuu --version
```

`make install` 使用 Go 的安装目录。如果找不到 `wuu` 且没有设置 `GOBIN`，将默认目录加入 shell 的 `PATH`：

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

如果设置了 `GOBIN`，应添加该目录。CLI 与桌面应用内置的 core 独立安装，版本可能不同。发布流程不提供独立 CLI 压缩包。产品标签使用日期版本，请从检出的源码安装，不要使用 `go install ...@latest`。

接下来[连接模型服务](model-services.md)。构建桌面应用请参阅[开发指南](../../en/project/development.md)（英文）。
