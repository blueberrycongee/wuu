# 安装 wuu

wuu 桌面预览版支持 Apple 芯片 Mac，自带运行所需的 core，无需另装 Go 或 CLI。

## 安装 macOS 桌面应用

1. 打开 [GitHub Releases](https://github.com/blueberrycongee/wuu/releases)。
2. 下载 `wuu-<version>-mac-arm64.dmg` 或 `wuu-<version>-mac-arm64.zip`。
3. 将 `wuu.app` 放入 `/Applications`。
4. 打开 wuu。

预览版使用固定的自签身份，没有 Apple Developer ID 和公证。确认下载来自官方 GitHub
Release 后，尝试打开 `/Applications/wuu.app`。如果被 macOS 拦截，前往**系统设置 →
隐私与安全性 → 仍要打开**。无需安装证书，也不要全局关闭系统安全保护。

## 从 GitHub Releases 更新

1. 从官方 Release 页面下载新版 DMG 或 ZIP。
2. 按 **Cmd+Q** 退出 Wuu，等待退出完成。关闭窗口不等于退出应用。
3. 用下载的应用替换 `/Applications/wuu.app`，保持名称和位置不变；不要从 DMG 或下载目录
   同时运行另一份 Wuu。
4. 打开 `/Applications/wuu.app`。会话和设置保存在 Wuu 用户数据中，升级不需要删除这些数据。

## 电脑操作权限

当前 GitHub Release 不包含 Computer Use 或原生 CUA 辅助程序。以下权限说明仅适用于
启用了 CUA 的源码构建。

使用 Computer Use 时，按提示在系统设置中授予**辅助功能**或**屏幕录制**权限。
Wuu 会提供设置入口，授权需由你完成；无需安装开发工具或自行签名。

升级后可能需要重新授权，尤其是从旧的未签名版本升级时。请授权当前的
`/Applications/wuu.app`，不要重置全部隐私权限或删除用户数据。

## 安装 CLI

需要终端或脚本调用时，安装 [go.mod](../../../go.mod) 要求的 Go 版本，再从源码构建：

```bash
git clone https://github.com/blueberrycongee/wuu.git
cd wuu
make install
wuu --version
```

找不到 `wuu` 命令时，将 Go 的二进制目录加入 `PATH`；如果设置过 `GOBIN`，请使用该目录：

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

确认生效后，可将设置加入 shell 启动文件。CLI 与桌面内置 core 独立，版本可能不同。
GitHub Releases 不提供独立 CLI 压缩包；产品的日期版本标签也不适用于
`go install ...@latest`，请使用上面的源码安装方式。

安装完成后，继续[连接模型服务](model-services.md)。
桌面源码构建见[开发指南](../../en/project/development.md)（英文）。
