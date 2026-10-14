# 控制手机

内置的 **Phone Harness** 插件让 Wuu 读取手机屏幕、打开 App、点击、滑动、滚动和输入。安装 CLI 并连接设备后，在 **Skills & Plugins** 中启用。插件默认关闭。

Wuu 通过公开插件 API 调用单独安装的 [Phone Harness](https://github.com/ShawnPana/phone-harness) CLI。插件支持 macOS 和 Linux。个人 iPhone 需要 macOS 的 iPhone 镜像；Android 使用 ADB；已有的 Phone Harness Cloud 会话可连接两种手机平台。

## 安装和连接

使用 [uv](https://docs.astral.sh/uv/) 安装支持的 CLI 版本：

```bash
uv tool install --python 3.12 phone-harness==0.3.2
phone-harness --version
```

Wuu 会在 PATH 和 `~/.local/bin` 中查找命令。如果安装在其他位置，请在启动 Wuu 前将 `WUU_PHONE_HARNESS` 设为可执行文件的绝对路径。插件要求 CLI 0.3.2，其他版本会在执行任务代码前被拒绝。插件不会自动安装或更新 Python 包。

| 设备 | 准备步骤 |
| --- | --- |
| 个人 iPhone | 在 Mac 上完成 Apple 的 iPhone 镜像配对，保持 iPhone 锁定。macOS 提示时，为控制进程授予辅助功能和屏幕录制权限。运行 `phone-harness --doctor ios` 排查缺失权限。 |
| Android | 安装 Android platform-tools，开启 USB 调试，连接或配对手机，并在手机上授权电脑。从 `adb devices -l` 复制准确的设备序列号。运行 `phone-harness --doctor android` 排查连接问题。 |
| 云手机 | 使用 Phone Harness 云端 CLI 登录并启动目标会话，从 `phone-harness cloud ls` 复制会话 ID。租用和计费由 Phone Harness 独立管理。 |

平台要求和连接排障见上游[安装指南](https://github.com/ShawnPana/phone-harness/blob/main/install.md)。登录认证、设备解锁、生物识别和配对需要用户操作。

## 在会话中使用

说明具体手机和任务，例如：“在我的镜像 iPhone 上打开设置，读取 iOS 版本”，或“在 Android 设备 SERIAL 上打开 App，查看它的设置”。Wuu 会加载内置手机 Skill，并使用 `phone_exec`。

每次调用都明确选择 `target`：`ios`、带 `serial` 的 `android`，或带 `session` 的 `cloud`。工具不会租用云手机。云端调用会拒绝 Phone Harness 工作目录中加载的 `PHONE_HARNESS_PLATFORM` 覆盖值，因为它可能把目标切换成本地设备；使用云会话前需移除该工作目录中的覆盖值。

工具接收 Python 代码，并预先导入上游手机操作函数。`print(screen())` 返回可见文本；`screenshot()` 会把本次调用中新写入的最后一张图片附加到结果中。可用函数包括 `open_app()`、`tap_text()`、`tap_image_point()`、`swipe()`、`scroll()` 和 `type_text()`。运行 `phone-harness skill` 可读取已安装版本的完整函数指南。

Python 变量不会跨调用保留，手机状态和上游 Agent 工作目录会保留。共享同一 Wuu 主目录的工作区会串行操作手机；单独运行的控制程序和不同 Wuu 主目录不受此锁约束。iPhone 镜像可能使用 Mac 的前台输入和剪贴板。

## 执行与限制

每次脚本默认允许执行 60 秒，`timeout_s` 可设为 1–300 秒。Wuu 保留每个输出流最后 64 KiB，并报告截断。单独占一行输出的新 PNG、JPEG 或 WebP 路径中，最后一张不超过 8 MiB 的图片会附加到结果。视觉模型可查看图片；纯文本模型依赖 OCR 或辅助功能结果。

停止任务或禁用插件会取消运行器及其活动子进程。已经送达的输入和远程请求仍可能产生效果。Wuu 不会自动重放中断的脚本；继续前需重新观察屏幕。禁用插件不会结束云手机租用。需要停止某个会话时，使用 `phone-harness cloud stop`。

与其他受信任插件一样，Phone Harness 及其 Python 脚本以本地用户权限运行。Wuu 会为这些调用关闭 Phone Harness 遥测。设备凭据和云端登录仍由 Phone Harness 管理。截图和屏幕文本会成为会话数据。读取屏幕不代表获得超出用户任务范围的发送、发布、购买或删除授权。

设备锁屏、Face ID、相机流程、DRM 视频，以及缺少可靠屏幕信息的控件，可能需要手动操作。插件不能让所有手机界面都自动化。
