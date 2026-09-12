# Wuu 原生手机客户端

**状态：原生首版已实现，发布验收未完成。** 手机负责账号、电脑目录、会话历史、发送消息和控制电脑上的任务。Agent、模型凭据、工具和工作区执行留在电脑。

iOS 使用 Swift 与 SwiftUI；Android 使用 Kotlin 与 Jetpack Compose。界面独立实现，遵循同一远程协议和恢复语义。旧 `clients/mobile`、`clients/mobile-web`、`clients/mobile-app` 手机实现已经停止开发，保留作交互与历史参考。

账号服务器保存用户明确开启同步后的对话文字副本。手机先读本地缓存，再按服务器 generation、cursor 和 revision 补齐变化。电脑离线时仍可读已同步历史；发送、停止与新建会话需要在线电脑。历史读取使用账号 HTTPS API，执行控制使用现有端到端加密远程通道，不把账号服务器变成 Agent 执行器。

登录保留服务器选择、GitHub 主入口和更多登录方式；进入电脑后保留侧滑会话列表、消息页顶部操作和系统返回顺序。账号令牌与设备身份使用系统安全存储，退出或凭据被撤销时清除账号会话及历史缓存。

## 当前能力

两端均实现 GitHub 浏览器登录及中断恢复、密码登录、注册和恢复密钥保存提示、电脑在线目录及账号设备移除。会话页支持工作区选择、新建、文字发送、流式更新、停止、排队消息展示，以及主机提问的选项和自由文本回答。左侧会话列表保留左滑展开置顶与归档操作，归档需要确认。

服务器历史与电脑实时会话合并展示。同步有明确的开启说明和关闭确认，本地缓存按服务器、账号、手机身份和电脑隔离；代际重置、删除记录、过期响应不会恢复已经清除的记录。恢复快照与后续实时通知按同一通道顺序更新界面。发送超时不自动重发，避免重复执行。

## 构建

iOS App 最低 iOS 17，使用 Xcode 打开 [ios/Wuu.xcodeproj](ios/Wuu.xcodeproj)，选择 `Wuu` scheme。模拟器构建不需要签名；真机安装需要在 Xcode 配置自己的开发团队。Swift Package 是可单独测试的通信与缓存模块，不是另一个 App。

```sh
xcodebuild -project clients/native/ios/Wuu.xcodeproj -scheme Wuu \
  -sdk iphonesimulator -derivedDataPath clients/native/ios/.build-xcode \
  CODE_SIGNING_ALLOWED=NO build
```

Android 最低 Android 9 / API 28，需要 JDK 17、Android SDK 36，配置 `JAVA_HOME` 和 `ANDROID_HOME`，或用 Android Studio 打开 [android](android)。APK 位于 `android/app/build/outputs/apk/debug/app-debug.apk`。

```sh
clients/native/android/gradlew -p clients/native/android :app:assembleDebug
adb install -r clients/native/android/app/build/outputs/apk/debug/app-debug.apk
```

在手机连接设置中填写电脑使用的 HTTPS 账号服务器，登录同一账号即可看到电脑。当前客户端依赖支持账号历史 API、`mobile-chat` 远程过滤和用户提问协议的 Wuu 主机版本。电脑不在线时不能发送或执行任务。Android App 禁止明文 HTTP；核心 JVM 测试使用的回环 HTTP 不代表生产网络策略。

## 自动验证

从仓库根目录运行以下命令，可选择 `ios`、`android` 或默认的 `all`。脚本先构建临时测试主机，再执行测试和 App 构建。

```sh
bash clients/native/verify.sh all
```

`testhost` 启动真实 Go relay、执行主机和 app-server，使用一次性身份、临时工作区和可控测试模型，不访问真实模型或用户配置。两端集成测试覆盖加密连接、发送、排队与撤回排队、快照与实时更新顺序、完成消息持久化，以及全新连接恢复相同会话。核心测试还覆盖 Go 加密向量与重放拒绝、服务器地址校验、历史代际切换、版本保护和删除记录。

也可以分别执行 `swift test --package-path clients/native/ios` 和 Gradle 的 `:app:testDebugUnitTest`。单独执行时必须设置 `WUU_NATIVE_TESTHOST` 为测试主机可执行文件的绝对路径；未设置会明确跳过真实主机集成测试。

## 验收边界

已经通过两端核心测试及真实主机集成测试，iOS 模拟器构建和 Android Debug APK 构建成功，并在本地模拟器安装启动。模拟器启动和核心集成测试不等于完整 UI 自动化验收，也不证明真机滚动帧率、键盘、VoiceOver/TalkBack 或后台恢复体验。

发布前仍需用实际 HTTPS 账号服务器验收 GitHub 登录、历史 REST 同步及设备撤销，再做 iPhone 和 Android 真机交互测试。当前以文字对话为范围；附件上传、完整 Markdown/工具详情、密码重置界面、推送通知、后台常驻连接和商店发布素材尚未实现。账号退出清除本机凭据与缓存；需要撤销服务器设备权限时使用账号设置中的“移除”。

该目录尚未配置发布签名、商店分发或正式移动端 CI 门禁。旧手机实现停止开发的标记不会删除原有代码，也不表示已有新的商店版本。
