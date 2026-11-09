# GitHub 登录：官方托管与自部署

桌面、手机和账号服务使用同一套开源实现。GitHub 只提供身份认证；电脑、设备撤销和加密远程连接仍由 Wuu 处理。GitHub 登录不会授予 Wuu 仓库、组织、Codespaces 或邮件权限，不替代电脑上的 Git/模型服务授权。

## 配置服务端

1. 在 GitHub **Settings → Developer settings → OAuth Apps → New OAuth App** 创建部署者自己的 OAuth App。
2. Homepage URL 填写账号服务的 HTTPS origin。Authorization callback URL 必须是 `https://你的账号域名/v1/account/github/callback`。
3. 将以下变量注入账号服务进程，Client Secret 只保存在服务端私密环境配置中：

```sh
WUU_ACCOUNT_PUBLIC_URL=https://你的账号域名
WUU_GITHUB_CLIENT_ID=你的客户端ID
WUU_GITHUB_CLIENT_SECRET=你的客户端密钥
```

本机开发允许 `http://127.0.0.1:8787`，回调地址也必须在 GitHub 中对应注册。这里的 origin 指账号服务，不是另外托管的网页地址。反向代理必须完整转发 `/v1/account/`，包括授权入口和回调，并支持已有 WebSocket 路径。

Compose 已接入上述变量，`WUU_ACCOUNT_PUBLIC_URL` 由 `WUU_DOMAIN` 构造。在受保护的部署环境文件中填写 `WUU_GITHUB_CLIENT_ID` 和 `WUU_GITHUB_CLIENT_SECRET`，再执行原有 Compose 命令。不要提交实际密钥。未配置时 GitHub 登录自动隐藏；只配一半或没有有效服务地址时服务端明确拒绝启动。

首次 GitHub 登录创建 Wuu 账号，需要 `--registration`。关闭注册后，已创建的 GitHub 账号仍可登录。用户身份按 GitHub 的不可变数字 ID 区分，改 GitHub 用户名不会变成新账号，也不会按邮箱或相同名字自动合并原有密码账号。切换电脑上的账号前，先正常退出旧账号。

自部署者使用自己的 OAuth App 和数据库，与官方实例独立。需要完全不依赖 GitHub 时，既有用户名密码、恢复码和独立二维码配对仍可使用。

## 配置官方客户端入口

官方域名未确定时不配置默认地址，客户端先展示服务器选择。域名就绪后，在构建进程设置公开变量：

```sh
WUU_ACCOUNT_SERVER=https://你的账号域名 npm --prefix desktop run build
WUU_ACCOUNT_SERVER=https://你的账号域名 npm --prefix clients/mobile-app run build
# 单独托管网页：
WUU_ACCOUNT_SERVER=https://你的账号域名 npm --prefix clients/mobile-web run build
```

这是可公开的服务地址，不是密钥。客户端优先使用用户之前选择的服务端；可以从“使用自部署服务器 / 更换服务器”切换。网页和手机构建共用入口，网页与账号服务可以在不同 origin。客户端读取选定服务器的 `/v1/account/config` 后才展示登录方式。手机浏览器中的 HTTP 局域网页面仍可用扫码配对；GitHub 登录请使用 HTTPS 网页或原生 App。

## 授权和恢复

点击“使用 GitHub 继续”后，在系统浏览器授权。原生 App 通过 `wuu://account/github` 返回；链接只唤起界面，不携带令牌。桌面轮询成功后回到 Wuu 窗口，网页保留原始页面并在独立标签页授权。

登录请求使用独立的随机领取凭据、10 分钟过期时间、GitHub PKCE 和浏览器回调 cookie。领取凭据保存在手机安全存储或电脑权限为 0600 的临时登录状态文件中，不放在回调 URL 里。客户端可以在冷启动后继续有效的授权请求；服务端重启会让未完成授权失效，需要重新开始，不影响已保存的登录。

授权后客户端用本地设备私钥签名登记，服务端签发 Wuu 自己的登录令牌。完成响应丢失时可以用同一设备重试。GitHub access token 仅在服务端换取身份时使用，不下发客户端、不持久化。

普通返回电脑列表保留连接凭据；扫码身份单独保存，可直接重新进入。只有“忘记此配对”删除扫码身份，账号退出和设备撤销仍按既有规则生效。设备列表暂时不可用时保留登录与最近电脑入口，并明确提示无法刷新。

## 验证与发布边界

`go test ./internal/remote/...` 在设置 `WUU_TEST_DATABASE_URL` 后验证真实 PostgreSQL 迁移、设备登记与认证。GitHub 集成测试使用本机模拟的 GitHub token/user 端点，不依赖生产凭据或真人账号。客户端测试覆盖服务器选择、授权取消、页面恢复和扫码返回。

安装 Playwright Chromium 后，`WUU_E2E_ACCOUNT_NAVIGATION=1 npm --prefix clients/mobile-web run test:e2e` 定向验证真实浏览器与 Go 中继、电脑之间的配对，以及返回、重新进入和冷启动后保留同一身份。

真实上线前还需要部署者配置实际 OAuth App，并在桌面、iOS、Android、网页各完成一次真实授权回调。仅完成源码和构建不表示官方服务已经上线。iOS App Store 的第三方登录要求仍需按实际分发方案处理，本阶段未实现 Apple 登录。

参考：[GitHub OAuth 授权流程](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps)。
