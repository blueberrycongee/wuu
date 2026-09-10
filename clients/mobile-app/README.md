# Wuu 手机 App

iOS 和 Android 使用打包在 App 内的共享 Wuu 界面，连接用户自己部署的账号服务。Agent、终端、文件和工作区都在所选电脑上运行。手机不代替电脑执行任务，也不会唤醒离线电脑。

功能对应与手机交互见 [功能清单](FEATURES.md)，本次证据和真实限制见 [验收记录](VALIDATION.md)。

## 构建环境

- Node.js 22 或更新版本、npm；Go 版本见根目录 `go.mod`。
- iOS：macOS、Xcode 26.0 或更新版本、已安装的 iOS Simulator runtime。真机需要自己的 Apple 开发签名 Team。
- Android：JDK 21、Android SDK Platform 36、Gradle 要求的 Build Tools（本次验证使用 35.0.0）、已接受 SDK 许可证。设置 `ANDROID_HOME`、`JAVA_HOME`。模拟器使用 API 36；App 最低版本由 `android/variables.gradle` 定义。

从仓库根目录安装依赖并构建：

```sh
npm --prefix desktop ci
npm --prefix clients/core ci
npm --prefix clients/mobile-web ci
npm --prefix clients/mobile-app ci
npm --prefix clients/mobile-app run build
```

`build` 先编译共享界面，再同步原生依赖和本地资源。不需要开发服务器、官方账号、Expo 服务或开发者私有密钥。不要在 Capacitor 配置中加入远程 `server.url`。

### iOS

```sh
xcodebuild -project clients/mobile-app/ios/App/App.xcodeproj \
  -scheme App -configuration Debug -sdk iphonesimulator \
  -derivedDataPath /tmp/wuu-ios CODE_SIGN_IDENTITY=- build
xcrun simctl install booted /tmp/wuu-ios/Build/Products/Debug-iphonesimulator/App.app
xcrun simctl launch booted com.blueberrycongee.wuu
```

模拟器也要保留 ad-hoc 签名，不要设置 `CODE_SIGNING_ALLOWED=NO`，否则 Keychain 无法正常工作。使用 `npm --prefix clients/mobile-app run ios` 打开工程，真机安装时在 Signing & Capabilities 选择自己的 Team；使用自己的 bundle identifier 需同时更新 Capacitor 配置及两平台工程。归档和分发使用自己的签名证书与描述文件，不把这些文件提交到仓库。

### Android

```sh
clients/mobile-app/android/gradlew -p clients/mobile-app/android assembleDebug
adb install -r clients/mobile-app/android/app/build/outputs/apk/debug/app-debug.apk
```

Debug APK 可直接安装到允许 USB 调试或侧载的设备。正式 APK/AAB 使用 Android Studio 的 Generate Signed Bundle / APK，以自己的 keystore 签名。后续升级必须使用同一签名；备份 keystore 和密码，仓库忽略这些文件。不要把 Debug 签名当成正式发行身份。

## 使用

已配置 GitHub 的服务端支持“使用 GitHub 继续”。先选择服务器，再在浏览器完成授权，App 会自动继续并保留登录进度；官方地址可在构建时配置。服务端和客户端配置见 [GitHub 登录](../../deploy/remote/GITHUB.md)。原有用户名密码入口位于“更多方式”。


1. 按 [自部署说明](../../deploy/remote/README.md) 启动账号服务。
2. 在电脑 Wuu 的「设置 → 手机访问」填写 HTTPS 服务端地址，创建账号并保存恢复码，或登录已有账号。保持电脑 Wuu 和网络运行。
3. 手机填写同一个服务端地址并登录同一账号。在线电脑可以打开；离线电脑显示离线。
4. 打开电脑后使用会话、项目、模型、工具、文件和 Git 等共享功能。顶部「电脑」返回设备列表，切换另一台电脑。
5. 从任一已登录设备移除丢失的设备。忘记密码时使用恢复码；恢复或修改密码会退出所有设备，需逐一重新登录。

本机模拟器验证可用 `http://127.0.0.1:端口`。Android 还需 `adb reverse tcp:端口 tcp:端口`。真机跨网络必须使用设备能够访问的 HTTPS 地址，不能将开发机 localhost 填入真机。正式部署配置受信任 TLS 证书。

## 本地数据与升级

后台提醒可直接连接部署者自己的 APNs/FCM，配置与签名步骤见 [系统推送](../../deploy/remote/PUSH.md)。未配置时设备页明确展示状态，回到前台仍会恢复真实会话。

手机的账号令牌和设备私钥保存在 iOS Keychain（仅本设备、解锁可读）或 Android Keystore AES-GCM 加密的私有存储中。Android 禁用应用备份。设备私钥不能靠复制 App 数据迁移，换机请重新登录。iOS 删除再安装 App 可能保留 Keychain 项；转交设备前先退出账号并在其他设备移除该设备。

导出文件保存在 App 私有缓存目录，以原文件名交给系统分享；下次启动或导出时清理超过 24 小时的缓存，退出账号时清除。系统分享目的地保存的副本由用户管理。完整文件导出上限为 64 MB，按 256 KB 分块读取；传输中检测到文件变更会报错，不能用截断预览代替完整文件。

账号退出及检测到撤销会清除手机保存的 Wuu 登录状态和账号相关界面偏好，保留语言选择与按服务端、账号隔离的设备私钥，让重新登录复用同一设备记录。清除应用存储或更换设备仍可能产生新记录；历史重复记录需手动移除，不能仅按名称合并。主动退出在离线时也会清除本地登录，并提示远端撤销未确认；刷新时的临时网络错误保留凭据以便恢复。历史的权威副本位于电脑；服务端仅存身份和设备目录，服务端备份不能恢复电脑上的会话与工作区。更新 App 前保留电脑数据，更新后可直接重连；恢复码与电脑数据备份说明见服务端文档。

## Android 真实链路测试

需要隔离验证数据时，先构建电脑端与 Go 核心，再用专用目录启动实际桌面入口：

```sh
go build -o /tmp/wuu-mobile-core ./cmd/wuu
npm --prefix desktop run build
WUU_HOME=/tmp/wuu-mobile-validation/state WUU_DESKTOP_CORE=/tmp/wuu-mobile-core \
  desktop/node_modules/.bin/electron clients/mobile-app/test/desktop.cjs
```

在该窗口完成正常引导、配置自己的模型并添加专用测试项目，再登录测试账号。脚本只隔离 Wuu 数据目录，终端仍使用当前操作系统用户；不要选择含重要资料的测试工作区。它加载实际生产入口、预加载脚本和打包界面，不替换桌面服务或 Agent。

先启动真实账号服务和已登录的电脑，配置一个能实际运行的模型，再运行：

```sh
adb reverse tcp:18787 tcp:18787
clients/mobile-app/android/gradlew -p clients/mobile-app/android \
  :app:connectedDebugAndroidTest \
  -Pandroid.testInstrumentationRunnerArguments.server=http://127.0.0.1:18787 \
  -Pandroid.testInstrumentationRunnerArguments.username=你的测试账号 \
  -Pandroid.testInstrumentationRunnerArguments.password=你的测试密码
```

使用专用测试账号和空白工作区。测试会发起真实 Agent 任务，创建 `android-live-proof.txt`，并验证后台返回与 Activity 重建。报告位于 `android/app/build/reports/androidTests/connected/debug/`。测试凭据是操作者提供的临时测试值，不要使用正式账号，Gradle 参数可能出现在本机进程列表中。

测试还会通过系统分享把电脑文件交给独立测试 APK，检查接收进程的包名、原文件名及完整内容。它不会向通讯录、邮件或云盘发送文件。图片选择可先准备公开测试素材，再增加 `-Pandroid.testInstrumentationRunnerArguments.image=wuu-validation.png`：

```sh
adb push desktop/build/icon.png /sdcard/Download/wuu-validation.png
adb shell am broadcast -a android.intent.action.MEDIA_SCANNER_SCAN_FILE \
  -d file:///sdcard/Download/wuu-validation.png
```

有多台电脑时增加 `-Pandroid.testInstrumentationRunnerArguments.computer=电脑显示名称`。测试截图位于 App 的外部私有 `files/validation` 目录；直接运行 instrumentation 后可用 `adb pull /sdcard/Android/data/com.blueberrycongee.wuu/files/validation` 获取。Gradle connected 测试可能在结束后卸载包，需保留截图时使用 `:app:assembleDebug :app:assembleDebugAndroidTest` 构建、`adb install -r` 安装两个 APK，再 `adb shell am instrument -w` 运行同一测试 runner。

传入 instrumentation 参数 `outage=true` 可验证实际电脑停止和重启。测试输出 `wuu_phase=ready-for-host-stop` 后关闭专用验证桌面；输出 `ready-for-host-start` 后重新启动同一 WUU_HOME 的桌面；测试检查断线提示、重连和未发送草稿保留，最后输出 `host-restored`。不要停止日常使用的桌面进程。此模式需要操作者或自己的测试控制器响应这些阶段，否则会明确超时。

## iOS 原生界面测试

在模拟器中登录专用测试账号并返回设备列表，然后运行长列表滑动测试。可使用自己模拟器的名称或 UUID：

```sh
xcodebuild -project clients/mobile-app/ios/App/App.xcodeproj -scheme AppUITests \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' \
  -derivedDataPath /tmp/wuu-ios-tests CODE_SIGN_IDENTITY=- \
  -only-testing:AppUITests/AccountUITests/testDeviceListCanReachAccountActions test
```

真实 Agent 测试从设备列表开始，会退出当前测试账号、登录指定账号、在第一台在线电脑的当前工作区创建 `ios-container-proof.txt`，再检查后台和冷启动恢复。通过环境变量提供临时测试账号，不要使用正式账号；Xcode 测试日志可能包含测试输入。

```sh
export TEST_RUNNER_WUU_TEST_SERVER=http://127.0.0.1:8787
export TEST_RUNNER_WUU_TEST_USERNAME=你的测试账号
export TEST_RUNNER_WUU_TEST_PASSWORD=你的测试密码
xcodebuild -project clients/mobile-app/ios/App/App.xcodeproj -scheme AppUITests \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' \
  -derivedDataPath /tmp/wuu-ios-tests CODE_SIGN_IDENTITY=- \
  -only-testing:AppUITests/AccountUITests/testAccountServerAgentRoundTrip test
```

结果和截图保存在 derived data 的 `Logs/Test/*.xcresult`。未提供环境变量时真实 Agent 测试会明确跳过，不代表链路通过。

扩展沿用电脑上的安装与信任状态：在手机的扩展目录中可启用、停用、配置和卸载，也可从电脑文件夹中安装扩展目录或 ZIP。已安装扩展的界面模块和图标通过加密连接读取并核对内容摘要，使用同一套公开 Extension API。扩展属于受信任代码，不提供手机端沙箱或额外审批。主应用资源仍随 App 打包。
