# 手机与自部署服务验收记录

本记录区分真实产品路径、可复现自动化测试和外部条件。验证日期为 2026-09-10。所有网络服务只在本机隔离环境运行，没有推送远端、发布 App 或部署公共服务。

## 环境与真实链路

- 服务端：从干净 Git 检出构建 Docker 镜像，在独立 Colima VM 中运行仓库 Compose，SQLite 持久卷、非 root UID 10001，Caddy 代理。验证脚本使用可信本地 CA 校验 HTTPS，没有关闭证书验证。模拟器应用通过 localhost 端口连接同一容器服务；跨互联网真机仍需部署者自己的有效 HTTPS 域名。
- 桌面：实际生产 Electron 主进程、预加载和打包界面，使用专用 WUU_HOME 与工作区，完成正常引导及设置登录。Go 核心和桌面服务池执行真实模型，手机没有替代执行器。
- iOS：iPhone 17 Pro 模拟器，iOS 26.2，Xcode 26.2，ad-hoc 签名。不是 iPhone 真机。
- Android：Pixel 7 配置模拟器，Android 16 / API 36 arm64，JDK 21，debug APK。不是 Android 真机。

## 已取得的证据

| 目标 | 验证结果 |
| --- | --- |
| 自部署可独立启动 | 干净源码构建镜像成功，Compose 启动后 healthz、HTTPS 账号 API 可用；更新镜像后原有账号仍能登录。 |
| 持久化、备份和恢复 | 停止容器后备份整个数据目录，重启验证持久化；撤销测试设备后恢复备份，验证备份时的账号和设备状态恢复。 |
| 账号隔离与撤销 | 两个真实测试账号的目录互不泄漏；跨账号删除拒绝；设备撤销、恢复码单次使用和改密撤销通过。账号/relay 的 race 测试通过。 |
| Android 到真实 Agent | 容器账号登录、发现电脑、真实模型在电脑生成 android-live-proof.txt，内容 ANDROID_VERIFIED；后台、Activity 重建和系统返回通过。 |
| iOS 到真实 Agent | 容器账号登录、发现电脑、真实模型在电脑生成 ios-container-proof.txt，内容 IOS_CONTAINER_VERIFIED；进入后台、返回前台及终止后冷启动恢复通过 XCUITest。 |
| 两端同一会话 | 手机与实际桌面共享运行和历史；iOS 恢复后看到 Android 发起的任务。共享 Web E2E 验证双向继续、桌面中断和远程关闭不终止任务。 |
| 真实电脑停止与重启 | 容器环境下停止实际桌面进程，iOS 目录显示离线且禁用打开；Android 显示断线。重启同一数据目录后恢复在线，Android 未发送草稿一致；原生阶段协调测试 43.644 秒通过。 |
| 手机工作区管理 | iOS 目录选择器在电脑创建并添加新项目，选择该项目后真实模型在新目录写入 PROJECT_VERIFIED，核对实际文件路径。 |
| 文件保存与分享 | iOS 系统“文件”保存原文件名，16 字节与电脑源文件逐字节相同。Android 通过系统 URI 授权交给独立测试 APK，断言接收包名、原文件名和完整内容。 |
| 图片 | iOS 系统照片库导入公开图标、真实视觉模型识别；Android 系统选择器导入同一公开图标，解码后显示在共享附件输入区。 |
| 终端 | iOS 打开电脑真实 PTY，执行 echo 并收到 IOS_TERMINAL_VERIFIED；手机顶部终端标签和补充控制键实际可用。 |
| 横屏、软键盘与安全区 | 两平台模拟器横屏导航和安全区复核。Android 实际 IME 将视口压到 122px 后，输入区和 44px 发送目标仍完整可见，原生测试检查真实几何。 |
| 长列表与长历史 | iOS 原生滑动到长设备列表底部账号操作通过；40 KB/s TCP 弱网 E2E 通过约 1.22 MB 工具历史、离线完成、快照/草稿恢复、宿主重启和 Git RPC。 |
| 扩展、Git、文件与协议边界 | 干净检出远程桌面相关测试 116 项通过，覆盖项目、文件、终端、技能、附件、Git、会话桥、布局、图片预览和手机设置。公开功能对应见 FEATURES.md。 |

弱网复验第一次在暂时恢复状态中直接请求 Git 时失败，第二次完整通过；保留这次时序波动。它不等同于真机蜂窝网络与 Wi-Fi 切换验收。模型稳定性和响应速度取决于部署者配置的模型服务。

## 验证方法与限制

原生测试使用真正安装的 App 和实际账号服务、电脑 Agent。Android 测试 APK 的分享接收页仅用于读取系统授予的文件 URI，不进入生产 App。Web E2E 使用确定性模型响应以覆盖工具与恢复边界；它不替代上述真实模型运行证据。

前台功能不要求推送凭据。APNs/FCM 服务端签名、请求格式、令牌登记、撤销和客户端错误状态有测试；真实平台送达尚未验证。完成该项需要部署者自己的 Apple 签名与 APNs 配置、Firebase 服务账号和 Android 配置，并按 PUSH.md 构建启用相应平台的安装包。不能将模拟器构建成功写成推送送达成功。

干净检出的客户端核心 62 项、Web 59 项、桌面相关 116 项测试通过，另有 1 项旧配对冒烟测试因未传入 WUU_SMOKE_URI 跳过，已另外运行完整 Web E2E。Go 账号/relay race、远程 CLI、分块文件协议测试和仓库 test-policy-check 通过。长设备列表同时在原布局与原生 XCUITest 下验证通过，未保留先前为排查模拟器拖动而尝试的布局修改。

本机设备查询没有发现 iPhone 真机，adb 仅列出模拟器。真机安装、相机实拍、蜂窝/Wi-Fi 切换、长时间后台策略和发行签名仍需实际设备与部署者签名条件。仓库提供两平台完整工程和构建说明，但没有可由他人复用的私有签名身份，也没有商店分发或官方托管承诺。

## 本任务实现与验收提交

以下为本任务本地提交；同一期间的无关桌面改动未列入本任务。没有推送远端。

```text
23ffaab67 feat(remote): add self-hosted accounts and isolated device routing
74f98b5e0 feat(desktop): connect account login to the shared remote execution host
0a62850bc feat(remote): add account clients and multi-computer selection credentials
3df947961 fix(mobile): remove duplicate workbench safe-area spacing
394c6cc41 fix(accounts): distinguish revoked credentials from temporary outages
077e345b1 feat(remote): expose desktop folders files and owned terminal sessions
2e3677933 build(remote): package the account service for self-hosted deployment
4a4e10a44 fix(mobile): keep touch drawers open through synthesized mouse events
b03bd6a70 test(remote): wait for service processes before removing fixture data
d92f9ce72 feat(mobile): add native iOS and Android account workbenches
c2354099c feat(mobile): route system back through workbench navigation
a27038ed7 feat(mobile): reuse trusted extension interfaces and skill content
86618b439 feat(mobile): share conversation images through native file actions
9a76fc1f8 build(ios): include the file access privacy manifest
7bbac2d51 fix(accounts): identify devices and show account-specific access controls
5baca1b69 test(deployment): verify account isolation and backup recovery on a live server
7e1e4ff9f test(mobile): add an isolated production desktop launcher
200eff96d fix(mobile): fit terminal controls to touch screens and avoid duplicate browser opens
cd0cd8b89 fix(mobile): preserve phone navigation and safe areas in landscape
5d8b64bde feat(mobile): export complete workspace files through bounded transfers
ab7bba257 feat(remote): deliver account notifications through operator-owned APNs and FCM
ebea53ddd fix(mobile): detach background transports while preserving session continuation
e13e97036 feat(mobile): register native push destinations and open verified computers
5261185a4 fix(mobile): retain exported files until system share consumers finish
d34ecd935 fix(mobile): keep landscape keyboard input and send controls visible
f717662d0 docs(mobile): map desktop capabilities to phone interactions and data boundaries
20115cf52 test(android): verify system file grants and image picker on a real desktop connection
848139de6 test(ios): exercise live account login, Agent execution and native restoration
103eb54b5 test(mobile): coordinate real desktop outages and verify independent share receivers
```

## 本次安装包

源码提交：`103eb54b5ee75f3ede624b03f7b8c733e1f54f45`，干净检出；移动版本 0.1.0。两平台构建及签名完整性检查通过。APK 使用 debug 签名，iOS 压缩包包含 arm64 模拟器 App，不能当作真机 IPA 安装。

| 文件 | SHA-256 |
| --- | --- |
| wuu-0.1.0-android-debug.apk | 871f7857fe14f3452ba6e78ccd6df742263384974c2df190b5ca3cdb003a135b |
| wuu-0.1.0-ios-simulator.zip | a2bcbbc4b5aff8c6fd3f66f7bb58298c948a751f9d541ffcaaa12ba84c483e34 |

这些校验值标识本次产物；重新构建时签名和构建元数据可能导致二进制校验值不同。构建和安装命令见 README.md，服务端部署、数据保护和恢复见 deploy/remote/README.md。
