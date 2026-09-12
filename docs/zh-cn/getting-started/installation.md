# 安装 wuu

wuu 当前提供 macOS 桌面预览版，也可以通过 Go 安装命令行工具。桌面应用适合交互式
工作；CLI 适合终端、脚本、CI 和其他 Agent。

## 安装 macOS 桌面应用

当前 GitHub Release 提供 Apple 芯片 Mac 使用的 arm64 DMG 和 ZIP：

1. 打开 [GitHub Releases](https://github.com/blueberrycongee/wuu/releases)。
2. 下载 `wuu-<version>-mac-arm64.dmg` 或 `wuu-<version>-mac-arm64.zip`。
3. 将 `wuu.app` 放入 `/Applications`。
4. 打开 wuu。

桌面安装包自带运行所需的私有 core，不需要另外安装 `wuu` CLI。

## 处理 macOS 的安全提示

当前预览版没有 Apple Developer ID 签名和公证，因此 Gatekeeper 可能阻止首次启动。
先确认文件来自项目的官方 GitHub Releases，再运行：

```bash
xattr -dr com.apple.quarantine /Applications/wuu.app
open /Applications/wuu.app
```

这条命令会移除下载文件的 quarantine 标记。不要对来源不明的应用运行它。

## 安装 CLI

产品版本使用日期版本，日期标签不是 Go 模块主版本。请从源码工作树构建 CLI，构建后检查版本：

```bash
git clone https://github.com/blueberrycongee/wuu.git
cd wuu
make install
wuu --version
```

GitHub Releases 不提供独立 CLI 压缩包。通过 Go 安装的 CLI 与桌面应用内置的 core
相互独立，可以同时存在，也可能处于不同版本。

### 找不到 `wuu` 命令

确认 Go 的二进制目录在 `PATH` 中：

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

如果这样可以找到 `wuu`，再把等价设置加入你的 shell 启动文件。

## 从源码运行

需要参与开发时，克隆仓库后可以直接运行 CLI：

```bash
git clone https://github.com/blueberrycongee/wuu.git
cd wuu
go run ./cmd/wuu --version
```

桌面开发环境和完整检查命令见[开发指南](../../en/project/development.md)（英文）。

## 下一步

安装完成后，继续[连接模型服务](model-services.md)。
