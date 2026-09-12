# Wuu 原生手机客户端

**状态：原生首版已实现，发布验收未完成。** 手机负责账号、电脑目录、会话历史、发送消息和控制电脑上的任务。Agent、模型凭据、工具和工作区执行留在电脑。

iOS 使用 Swift 与 SwiftUI；Android 使用 Kotlin 与 Jetpack Compose。界面独立实现，遵循同一远程协议和恢复语义。旧 `clients/mobile`、`clients/mobile-web`、`clients/mobile-app` 手机实现已经停止开发，保留作交互与历史参考。

账号服务器保存用户明确开启同步后的对话文字副本。手机先读本地缓存，再按服务器 generation、cursor 和 revision 补齐变化。电脑离线时仍可读已同步历史；发送、停止与新建会话需要在线电脑。历史读取使用账号 HTTPS API，执行控制使用现有端到端加密远程通道，不把账号服务器变成 Agent 执行器。

登录保留服务器选择、GitHub 主入口和更多登录方式；进入电脑后保留侧滑会话列表、消息页顶部操作和系统返回顺序。账号令牌与设备身份使用系统安全存储，退出或凭据被撤销时清除账号会话及历史缓存。

## 当前能力

两端均实现 GitHub 浏览器登录、返回 App、中断恢复与取消，密码登录、注册、修改密码和恢复密钥找回。重置密码明确确认退出所有设备，并保存新恢复密钥。退出登录会请求服务器撤销此手机，再清除本机数据；离线撤销失败时提示使用其他设备移除。

电脑目录会定时更新，并按当前登录保存离线入口；重启后断网仍可进入已缓存的电脑记录，联网后重新检查设备权限。会话页支持工作区选择、新建、发送、流式更新、停止、排队和暂停消息的恢复与移除，以及主机提问的选项和自由文本回答。左侧支持全文搜索、重命名、置顶、归档与恢复；归档需要确认。

会话设置支持选择电脑上已配置的模型、推理配置和执行权限，只作用于当前会话。修改需要电脑在线且任务空闲；不受限访问需要确认。外部执行引擎的设置仍在电脑上管理。

在线长历史按页读取，翻页保留正在更新的回复。超长消息先显示预览，可明确请求完整内容；读取分块校验，超过手机内存预算时提示在电脑查看。对话可导出为文本文件，历史未全部加载时入口明确标为导出已加载文本。

工具调用显示执行中、完成或失败状态，可展开参数、文字结果与错误。主机发送有大小限制的开始和完成记录，超长结果通过同一完整内容接口按需读取；不发送工具输出增量。工具详情不进入账号服务器历史或对话文字导出。扩展的自定义桌面界面、结果中的非文字内容仍需在电脑查看。

助手消息使用原生 Markdown 排版，支持代码、列表、表格和网页链接；超过 128 KB 的单条文本使用普通文本显示。iOS 使用 MarkdownUI 2.4.1，Android 使用 Markwon 4.6.2 的原生 TextView，不使用 WebView。Markdown 不自动下载外部图片，链接仅打开 HTTP/HTTPS 地址。MarkdownUI 已进入维护期；其后继 Textual 要求 iOS 18，本客户端目前支持 iOS 17。

账号设置中的开源许可可离线阅读；两端安装包包含同一份依赖署名和许可文本，位于 [licenses](licenses)。更新依赖时同步核对这份清单。

通知使用部署方提供的 APNs / FCM。手机按当前登录单独保存同意状态，支持开启、重新注册和关闭；关闭失败会保留关闭意图并在回到前台时重试。点击通知后重新读取当前账号的电脑目录，再进入仍有权限的电脑。通知只包含通用提醒和电脑标识，不包含对话内容。

系统照片与文件选择器支持图片和 PDF。图片保留相机方向并缩至最长边 1600 像素，转换为 JPEG；源图片限制为 32 MB、4000 万像素。每条消息最多 4 个附件，处理后的内容合计不超过 3 MB，文字不超过 128 KB。附件通过加密通道发给电脑，不进入账号服务器文字历史。

点击消息附件后才读取内容，分块校验原始摘要，单个附件最多读取约 12 MB。图片预览在解码前检查像素上限并缩小；iOS 使用 Quick Look 查看图片和 PDF，Android 使用原生图片组件并交给设备上的阅读器打开 PDF。两端支持保存或分享原文件；切换会话或账号后，过期读取不会打开旧附件。

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

在手机连接设置中填写电脑使用的 HTTPS 账号服务器，登录同一账号即可看到电脑。当前客户端依赖支持账号历史 API、`mobile_activity` 远程过滤和用户提问协议的 Wuu 主机版本；旧 `mobile_chat` 文本过滤的行为保持不变。电脑不在线时不能发送或执行任务。Android App 禁止明文 HTTP；核心 JVM 测试使用的回环 HTTP 不代表生产网络策略。

### 可选推送配置

默认构建不开启推送注册。账号服务器必须通过 `/v1/account/config` 的 `push_platforms` 声明对应平台，并配置自己的 APNs / FCM 发送凭据。客户端通过认证的 `/v1/account/push` 管理此手机的注册；不使用旧 Expo 推送入口。

iOS 签名构建需启用 Push Notifications capability，并设置 `WUU_PUSH_ENABLED=YES`、`CODE_SIGN_ENTITLEMENTS=Config/Push.entitlements` 和 `WUU_APNS_ENVIRONMENT=development` 或 `production`。开发团队、Bundle ID、provisioning profile 必须支持该 entitlement；服务器 APNs topic 与环境必须对应。APNs 私钥只放在服务器。

Android 构建可通过同名 Gradle 属性或环境变量提供 `WUU_FIREBASE_APP_ID`、`WUU_FIREBASE_API_KEY`、`WUU_FIREBASE_PROJECT_ID`、`WUU_FIREBASE_SENDER_ID`。这些值是 Firebase Android App 的公开客户端配置，不是 service-account 私钥；不需要 `google-services.json` 或 Google Services Gradle 插件。未配置时仍可构建和使用其他功能，设备没有 Google Play 服务时不能启用 FCM。SDK 自动注册和 Analytics 收集默认关闭，用户明确开启后才获取 token；当前使用 Firebase Messaging 25.0.1 的 registration-token API，与服务器契约保持一致。

两端回到前台时重新获取 token 并更新服务器。Android 后台轮换的 token 在下次回到前台时上传，这段时间通知可能无法送达；不把推送当作消息同步或任务完成的权威来源。Android 构建自动打包 Google SDK 原始依赖许可，并在许可页面按段加载。

## 自动验证

从仓库根目录运行以下命令，可选择 `ios`、`android` 或默认的 `all`。需要安装 PostgreSQL 服务端工具；`pg_config --bindir` 用于定位，或设置 `WUU_TEST_PG_BIN`。脚本创建自己的临时数据库集群，只监听临时 Unix socket，结束时停止并删除，不连接已有数据库。

```sh
bash clients/native/verify.sh all
```

`testhost` 启动真实 Go relay、执行主机和 app-server，使用一次性身份、临时工作区和可控测试模型，不访问真实模型或用户配置。两端集成测试复用界面的发送参数，覆盖图片与 PDF 发送、排队撤回、暂停恢复、搜索归档、快照与实时更新顺序、持久化和全新连接恢复。预置的旧会话覆盖历史翻页、超长 Unicode 消息和工具结果的完整读取。核心测试还覆盖 Go 加密向量与重放拒绝、服务器地址校验、历史代际切换、版本保护、删除记录、工具完成与迟到读取的竞争，以及输入资源上限。

`testaccount` 使用生产账号 HTTP handler 和 PostgreSQL，覆盖注册、修改密码、恢复密钥轮换、全部设备撤销和服务器退出，以及历史跨页同步、磁盘恢复、编辑失效、删除与延迟响应、跨账号隔离。OAuth 测试仅替代外部 GitHub 响应，实际执行服务器的浏览器 cookie、PKCE、账号绑定和设备签名验证，并覆盖取消、拒绝、恢复和重复完成。

也可以分别执行 `swift test --package-path clients/native/ios` 和 Gradle 的 `:app:testDebugUnitTest`。独立运行的集成测试需要 `WUU_NATIVE_TESTHOST`、`WUU_NATIVE_TESTACCOUNT` 可执行文件路径和临时数据库；缺少环境会明确跳过对应集成测试，完整验收请使用上述脚本。

模拟器 UI 测试入口如下。iOS 脚本创建并删除专用模拟器，隔离 Keychain 与 App 数据，并使用临时签名运行；Android 使用包名后缀为 `.uitest` 的独立构建，只对该测试 App 开放回环 HTTP。`testaccount -live` 启动完整账号、relay 和电脑执行主机，仅替换模型提供方。UI 测试操作正常服务器设置和登录界面，不使用 App 内测试后门。两端均已通过登录、选择电脑、新建、发送、流式回复、实际工具执行与展开结果、停止、前后台重连、会话设置保存和侧栏开关流程。

```sh
python3 clients/native/ui-test.py ios
python3 clients/native/ui-test.py android --device emulator-5554
```

## 验收边界

本地完整验证通过 Swift 15 项、Android 16 项核心及集成测试，无失败或跳过；上述两端 UI 流程也已通过。iOS 模拟器、未签名的 iOS 真机 Release、Android Debug APK 和未签名 Release APK/AAB 均构建成功，Android Release lint 无错误。本地 UI 测试环境为 iOS 26.2 和 Android 16 / API 36。这些结果不证明真机滚动帧率、键盘、VoiceOver/TalkBack 或全部主题和屏幕尺寸的体验；尚未完成人工视觉验收。

发布前仍需验证部署的 HTTPS、真实 GitHub OAuth 配置和系统浏览器返回，再做 iPhone 和 Android 真机交互测试。推送注册、轮换、关闭与撤销已通过本地账号服务测试；APNs / FCM 实际送达、点击和系统权限需要配置签名及服务凭据后联调。商店图标、截图、隐私政策与发布签名尚未准备。手机后台会断开执行通道，回到前台重新连接并恢复，不承诺后台常驻连接。

iOS 安装包声明本机偏好设置，以及缓存和用户选择文件的元数据访问理由；商店的数据收集声明仍需按实际服务器部署填写。Android 明确排除云备份和换机数据迁移，避免把设备身份、推送同意和历史缓存复制到另一部手机。

Native mobile 工作流运行核心集成、未签名 Release 构建、Android Release lint 及 iOS 26.2 / Android API 28、36 UI 流程，保存失败报告和 iOS 截图。工作流已加入仓库，尚未在 GitHub 执行验证；临时 PostgreSQL 不连接已有数据库，也不需要账号或模型密钥。发布签名、商店分发和分支保护门禁仍需单独配置。旧手机实现停止开发的标记不会删除原有代码，也不表示已有新的商店版本。
