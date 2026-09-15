# Wuu 原生手机客户端

**状态：原生首版已实现，发布验收未完成。** 手机负责账号、电脑目录、会话历史、发送消息和控制电脑上的任务。Agent、模型凭据、工具和工作区执行留在电脑。

iOS 使用 Swift 与 SwiftUI；Android 使用 Kotlin 与 Jetpack Compose。聊天、导航、键盘和附件操作使用原生界面，遵循同一远程协议和恢复语义。Agent 小球与群头像直接打包电脑端的 React/SVG 组件，在无网络、无原生权限桥接的小型 WebView 中显示；动画状态、形状和配饰不在手机重复实现。旧 `clients/mobile`、`clients/mobile-web`、`clients/mobile-app` 手机实现已经停止开发，保留作交互与历史参考。

账号服务器保存用户明确开启同步后的对话文字副本。手机先读本地缓存，再按服务器 generation、cursor 和 revision 补齐变化。电脑离线时仍可读已同步历史；发送、停止与新建会话需要在线电脑。历史读取使用账号 HTTPS API，执行控制使用现有端到端加密远程通道，不把账号服务器变成 Agent 执行器。

登录保留服务器选择、GitHub 主入口和更多登录方式；进入电脑后保留侧滑会话列表、消息页顶部操作和系统返回顺序。账号令牌与设备身份使用系统安全存储，退出或凭据被撤销时清除账号会话及历史缓存。

## 当前能力

进入电脑后可在底部切换“会话”和“协作”，默认打开协作。两种模式共用加密连接，独立保留当前对话、草稿和阅读位置；App 重启后恢复上次的电脑、工作区、模式和会话或房间，恢复位置仅对原登录及仍有权限的电脑有效；切换页面不停止电脑任务。协作支持查看房间、与已有 Agent 私聊、创建群聊、收发文字和发送图片/PDF、系统拍照，成员与已加载任务在详情页查看。工作状态使用共享小球表示，失败或中断后可以继续原会话；收到的图片自动显示缩略图，点击可查看原图；Agent 配置和创建申请处理仍需在电脑完成，手机会显示相应入口限制。

协作仅在前台选中该模式时刷新公开房间消息，不把 Agent 私有执行事件正文放进群聊。每次读取最近 30 条，支持向前翻页和重连补齐缺失消息；已加载的较早任务会轮流刷新状态。离线保留本次已加载的内容只读，不新增账号服务器同步副本；退出电脑、退出账号或失去房间访问后清理相关内容。发送失败不自动重试，请先检查房间是否已有该消息。

此协作实现要求电脑支持 `channel/message/list` 的 `latest`、`before_seq` 和 `attachment_metadata_only` 参数，以及 `channel/attachment/read`、`message/image/read`。`latest` 选取最新窗口但仍按序号升序返回，`before_seq` 是排除边界；默认调用的旧语义保持不变。手机只请求附件元信息，避免房间历史中的内嵌附件超过远程帧限制；升级手机时也应更新电脑端。

两端均实现 GitHub 浏览器登录、返回 App、中断恢复与取消，密码登录、注册、修改密码和恢复密钥找回。重置密码明确确认退出所有设备，并保存新恢复密钥。退出登录会请求服务器撤销此手机，再清除本机数据；离线撤销失败时提示使用其他设备移除。

电脑目录会定时更新，并按当前登录保存离线入口；重启后断网仍可进入已缓存的电脑记录，联网后重新检查设备权限。会话页支持工作区选择、新建、发送、流式更新、停止、排队和暂停消息的恢复与移除，以及主机提问的选项和自由文本回答。左侧支持全文搜索、重命名、置顶、归档与恢复；归档需要确认。

会话设置支持选择电脑上已配置的模型、推理配置和执行权限，只作用于当前会话。修改需要电脑在线且任务空闲；不受限访问需要确认。外部执行引擎的设置仍在电脑上管理。

在线长历史按页读取，翻页保留正在更新的回复。超长消息先显示预览，可明确请求完整内容；读取分块校验，超过手机内存预算时提示在电脑查看。对话可导出为文本文件，历史未全部加载时入口明确标为导出已加载文本。

工具调用显示执行中、完成或失败状态，可展开参数、文字结果与错误。主机发送有大小限制的开始和完成记录，超长结果通过同一完整内容接口按需读取；不发送工具输出增量。工具详情不进入账号服务器历史或对话文字导出。工具结果中的图片复用消息图片预览；扩展的自定义桌面界面和其他非文字内容仍需在电脑查看。

助手消息使用原生 Markdown 排版，支持代码、列表、表格和网页链接；超过 128 KB 的单条文本使用普通文本显示。iOS 使用 MarkdownUI 2.4.1，Android 使用 Markwon 4.6.2 的原生 TextView，聊天文字不使用 WebView。Agent 回复中明确以 Markdown 图片语法引用的工作区图片，通过同一加密通道读取；代码块和普通文件链接不会触发图片读取。Markdown 不自动下载外部网络图片，网页链接仅打开 HTTP/HTTPS 地址。MarkdownUI 已进入维护期；其后继 Textual 要求 iOS 18，本客户端目前支持 iOS 17。

账号设置中的开源许可可离线阅读；两端安装包包含同一份依赖署名和许可文本，位于 [licenses](licenses)。更新依赖时同步核对这份清单。

通知使用部署方提供的 APNs / FCM。手机按当前登录单独保存同意状态，支持开启、重新注册和关闭；关闭失败会保留关闭意图并在回到前台时重试。点击通知后重新读取当前账号的电脑目录，再进入仍有权限的电脑。通知只包含通用提醒和电脑标识，不包含对话内容。

系统照片与文件选择器支持图片和 PDF；相机入口使用系统拍照界面。选择后立即显示照片草稿缩略图。图片保留相机方向并缩至最长边 1600 像素，转换为 JPEG；源图片限制为 32 MB、4000 万像素。每条消息最多 4 个附件，处理后的内容合计不超过 3 MB，文字不超过 128 KB。附件通过加密通道发给电脑，不进入账号服务器文字历史。

屏幕内的消息图片自动读取最长边 384 像素的缩略图，每端最多同时读取两张、缓存 48 张。点击图片才分块读取原图并校验摘要，单个附件最多读取约 12 MB。图片在解码前检查像素上限并缩小；iOS 使用 Quick Look 查看图片和 PDF，Android 使用支持缩放的原生图片组件并交给设备上的阅读器打开 PDF。两端支持保存或分享原文件；切换会话或账号后，过期读取不会打开旧附件。

服务器历史与电脑实时会话合并展示。同步有明确的开启说明和关闭确认，本地缓存按服务器、账号、手机身份和电脑隔离；代际重置、删除记录、过期响应不会恢复已经清除的记录。恢复快照与后续实时通知按同一通道顺序更新界面。历史同步与执行连接分别恢复，慢历史请求不会阻塞发消息；重新连接保留已显示内容，单个会话读取失败不会主动关闭整条连接。发送超时不自动重发，避免重复执行。

## 构建

原生安装包使用仓库中已生成的共享头像资源，普通原生构建不要求安装 Node.js。修改电脑端头像组件后，在安装好 `desktop` 依赖的仓库中运行：

```sh
node clients/native/shared-ui/build.mjs
node clients/native/shared-ui/build.mjs --check
```

两端构建会检查共享资源与实际组件源文件的摘要，过期时提示重新生成。入口与边界见 [shared-ui](shared-ui/README.md)。

iOS App 最低 iOS 17，使用 Xcode 打开 [ios/Wuu.xcodeproj](ios/Wuu.xcodeproj)，选择 `Wuu` scheme。模拟器运行使用 Xcode 自动生成的本地临时签名，无需开发团队；真机安装需要在 Xcode 配置自己的开发团队。Swift Package 是可单独测试的通信与缓存模块，不是另一个 App。

```sh
xcodebuild -project clients/native/ios/Wuu.xcodeproj -scheme Wuu \
  -sdk iphonesimulator -derivedDataPath clients/native/ios/.build-xcode \
  build
```

仅检查编译时可以添加 `CODE_SIGNING_ALLOWED=NO`；该未签名产物不能用于完整模拟器验收，系统钥匙串访问会失败。

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

`testaccount -live` 可启动供模拟器登录的隔离账号与主机，但默认独立执行模式的任务由手机 app-server 连接持有，不能据此验收桌面任务的后台持续运行。验证桌面生命周期时，先启动仓库的 `desktop/test-fixtures/sharedRemoteHost.ts` 共享服务池，再使用 `testaccount -live -desktop`，显式传入其 `WUU_DESKTOP_APP_SERVER_ADDR`、`WUU_DESKTOP_APP_SERVER_TOKEN` 和临时 `WUU_TEST_WORKSPACE`。模型仍可使用本地受控服务；服务池、远程桥接、执行、持久化和原生 UI 使用产品代码。未指定 `-desktop` 时忽略环境中的桌面端点，避免测试连接到日常桌面。

模拟器上的整段登录/发送/前后台流程会跟着界面一起变，不适合作为日常门禁。CI 只跑核心集成、未签名 Release 构建和 Android lint。

## 验收边界

自动验证与模拟器视觉检查分别记录，不将编译或协议测试视为真机验收。发布前需要使用本次代码重新运行完整验证，并检查真机滚动帧率、键盘、VoiceOver/TalkBack、主题和屏幕尺寸。共享头像的 WebView 首次加载与多头像列表的内存表现也需覆盖真机。

发布前仍需验证部署的 HTTPS、真实 GitHub OAuth 配置和系统浏览器返回，再做 iPhone 和 Android 真机交互测试。推送注册、轮换、关闭与撤销已通过本地账号服务测试；APNs / FCM 实际送达、点击和系统权限需要配置签名及服务凭据后联调。商店图标、截图、隐私政策与发布签名尚未准备。手机后台会断开执行通道，回到前台重新连接并恢复，不承诺后台常驻连接。

iOS 安装包声明本机偏好设置，以及缓存和用户选择文件的元数据访问理由；商店的数据收集声明仍需按实际服务器部署填写。Android 明确排除云备份和换机数据迁移，避免把设备身份、推送同意和历史缓存复制到另一部手机。

Native mobile 工作流运行核心集成、未签名 Release 构建和 Android Release lint。临时 PostgreSQL 不连接已有数据库，也不需要账号或模型密钥。发布签名、商店分发和分支保护门禁仍需单独配置。旧手机实现停止开发的标记不会删除原有代码，也不表示已有新的商店版本。
