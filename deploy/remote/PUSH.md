# 自部署系统推送

Wuu 账号中继可直接调用 APNs（iOS）和 FCM HTTP v1（Android），代码在本仓库。使用部署者自己的 Apple/Firebase 项目，不经过 Expo 或 Wuu 官方推送服务。未配置推送不影响登录、远程 Agent 和回到前台后的状态恢复；手机明确显示推送未配置。

## 服务端

创建只允许服务端用户读取的配置文件，例如 `/private/push.json`：

```json
{
  "apns": {
    "key_file": "/private/AuthKey.p8",
    "key_id": "YOUR_KEY_ID",
    "team_id": "YOUR_TEAM_ID",
    "topic": "your.own.wuu.bundle.id",
    "sandbox": true
  },
  "fcm": {
    "service_account_file": "/private/firebase-service-account.json"
  }
}
```

只需要一个平台时删除另一个平台的配置。APNs 使用 Apple Developer 创建的 P-256 `.p8` 认证密钥；`topic` 必须匹配安装包的 bundle identifier。Debug 开发签名使用 sandbox；TestFlight/App Store 使用 production（`sandbox: false`）。FCM JSON 使用自己的 Firebase 项目服务账号，开启 Firebase Cloud Messaging API，并授予发送消息所需权限。

在账号服务的参数中添加 `--push-config /private/push.json`。Compose 可用本地 override：

```yaml
services:
  accounts:
    command: ["--registration", "--push-config", "/private/push.json"]
    volumes:
      - ./private-push:/private:ro
```

该目录内放配置和密钥，文件权限保证容器 UID 10001 可读。不要提交、公开分享或挂载进手机包。轮换凭据后重启账号服务；注册的手机设备无需重新登录。APNs JWT 和 FCM OAuth 令牌在内存中缓存并自动更新。发送超时、提供方拒绝及队列满会写不含令牌的服务端诊断日志；提醒采用有限并发、每设备限速，不阻塞会话执行。

## 安装包

iOS 使用自己的支持 Push Notifications 的 App ID、签名 Team 和描述文件。构建主界面时启用该平台：

```sh
VITE_WUU_PUSH_PLATFORMS=ios npm --prefix clients/mobile-app run build
xcodebuild -project clients/mobile-app/ios/App/App.xcodeproj -scheme App \
  -configuration Debug -sdk iphoneos \
  DEVELOPMENT_TEAM=YOUR_TEAM PRODUCT_BUNDLE_IDENTIFIER=your.own.wuu.bundle.id \
  CODE_SIGN_ENTITLEMENTS=App/Push.entitlements APS_ENVIRONMENT=development build
```

正式归档改用 Release 和 `APS_ENVIRONMENT=production`，并匹配服务端 sandbox 设置。普通不带推送的模拟器/个人签名构建不需要此 entitlement。使用 Xcode 图形界面时设置相同的 Code Signing Entitlements 和 APS_ENVIRONMENT。

Android 在自己的 Firebase 项目登记包名，下载 `google-services.json` 放到 `clients/mobile-app/android/app/`，然后：

```sh
VITE_WUU_PUSH_PLATFORMS=android npm --prefix clients/mobile-app run build
clients/mobile-app/android/gradlew -p clients/mobile-app/android :app:assembleDebug
```

同时配置两平台时使用 `VITE_WUU_PUSH_PLATFORMS=ios,android`。构建会拒绝缺少 Android Firebase 配置的推送包，避免运行时假装注册成功。更改应用标识时同步修改 Capacitor、原生工程和 Firebase/Apple 配置。客户端 Firebase 配置与服务端服务账号密钥不同；服务账号私钥绝不能打入 App。

登录后在电脑列表下方开启「系统通知」，授予操作系统通知权限。App 取得平台令牌后通过自己的账号身份登记，只能更改当前手机的推送目的地。再次启动和恢复网络会重新登记轮换后的令牌；关闭通知会先删除服务端目的地再注销系统令牌。退出账号、撤销手机、改密码或恢复账号会删除相关登记。点击通知时只会打开当前账号目录中仍存在的电脑。

## 保护范围与验收

服务端额外保存平台名和推送目的地令牌；设备删除会级联删除。Apple/Google 可见固定提醒文案、电脑公钥标识、令牌、投递时间，不会收到会话正文、文件、模型回复或工具参数。通知不能唤醒离线电脑，也不保证绕过操作系统的通知关闭、省电或强制停止规则。

使用真实签名安装包、自己的平台凭据验证：开启通知，把 App 切到后台，从电脑发起真实任务，检查完成/需要输入提醒及点击后的电脑选择，再撤销该手机并确认不再投递。协议测试验证签名和路由；模拟器构建通过不能代替 APNs/FCM 实际投递证据。

平台配置依据 [Capacitor 8 Push Notifications](https://capacitorjs.com/docs/apis/push-notifications)、[Apple APNs 请求协议](https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns)、[FCM HTTP v1](https://firebase.google.com/docs/cloud-messaging/send/v1-api)。
