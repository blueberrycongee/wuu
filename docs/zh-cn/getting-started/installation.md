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

## 打开 macOS 预览版

预览版使用固定的自签身份，没有 Apple Developer ID 和公证。确认下载来自官方 GitHub
Release 后，尝试打开 `/Applications/wuu.app`。如果被 macOS 拦截，前往**系统设置 →
隐私与安全性 → 仍要打开**。无需安装证书，也不要全局关闭系统安全保护。

## 从 GitHub Releases 更新

1. 从官方 Release 页面下载新版 DMG 或 ZIP。
2. 按 **Cmd+Q** 退出 Wuu，等待退出完成。关闭窗口不等于退出应用。
3. 用下载的应用替换 `/Applications/wuu.app`，保持名称和位置不变；不要从 DMG 或下载目录
   同时运行另一份 Wuu。
4. 打开 `/Applications/wuu.app`。会话和设置保存在 Wuu 用户数据中，升级不需要删除这些数据。

Wuu 退出前会等待 core 和电脑操作预览停止。CUA helper 位于应用包内部，随应用一起替换，
无需另外卸载特权 helper 或登录服务。

## 电脑操作权限

macOS 版自带 Computer Use。首次使用时，请在系统设置中授予所需的**辅助功能**或
**屏幕录制**权限。Wuu 会报告缺失的权限并提供设置入口，无法代替用户授权。
用户无需安装开发工具或自行签名。

发布签名保持各版本的应用身份稳定，但不保证所有 macOS 版本都保留授权。从旧的未签名或
ad-hoc 版本首次升级时，可能需要再次授权。如果升级后失去权限，请按错误提示进入对应的
系统设置，授权当前的 `/Applications/wuu.app`。不要重置全部隐私权限或删除用户数据。
系统权限列表里的旧条目与磁盘文件不同；如需移除，只处理已确认过时的条目。

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
