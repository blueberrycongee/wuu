# 自部署 Wuu 账号与连接服务

服务端、手机客户端和桌面接入代码都在本仓库。无需 Wuu 官方账号、云服务、开发者密钥或邮件服务。电脑仍是 Agent 的执行与会话持久化来源；电脑离线时，服务端不会代执行或唤醒电脑。

## GitHub 登录与官方托管

同一套服务端支持可选的 GitHub 登录。部署者配置自己的 OAuth App；官方构建可设置默认服务地址，用户也可选择自部署实例。完整配置、回调与验证步骤见 [GitHub 登录](GITHUB.md)。未配置 GitHub 时，既有账号密码与扫码流程保持可用。

## 本地启动

安装仓库要求的 Go 1.26.5 和 PostgreSQL 18，创建专用数据库与拥有该库建表权限的账号，再运行：

```sh
go build -trimpath -o ./bin/wuu-server ./cmd/wuu-server
export WUU_DATABASE_URL='postgres://wuu:YOUR_URL_ENCODED_PASSWORD@127.0.0.1:5432/wuu?sslmode=disable'
mkdir -p ./private-remote-data
chmod 700 ./private-remote-data
./bin/wuu-server --addr 127.0.0.1:8787 \
  --state ./private-remote-data/relay.json \
  --registration
```

`wuu-server` 是独立 Go 程序，只链接账号、加密连接及服务端代码，不包含 Agent 执行引擎。默认监听 `127.0.0.1:8787`，必须通过 `WUU_DATABASE_URL` 或 `--database-url` 配置 PostgreSQL，旧配对注册表为 `data/relay.json`；注册默认关闭，首次创建账号需显式传 `--registration`。`--help` 列出 TLS、可信代理、推送和 Web 资源参数。`wuu relay` 调用同一实现，未配置数据库时仍可运行纯 QR relay。

数据库 URL 包含凭据，优先放入受保护的环境配置，避免出现在命令参数中。远程数据库使用 `sslmode=verify-full` 和可信 CA；示例的 `disable` 仅用于本机或 Compose 内部网络。

完整 API 和代码入口见 [服务端接口](API.md)。

`GET /healthz` 返回 `ok`。从电脑和手机的账号页选择此服务端、创建账号，然后在另一端登录同一账号。用户名为 3–64 个小写字母、数字、点、连字符或下划线；密码至少 12 字节。请保存注册时显示的恢复码。

`http://127.0.0.1` 只适用于本机测试。Android 模拟器通过 `adb reverse tcp:8787 tcp:8787` 访问电脑的 localhost；iOS 模拟器直接访问电脑 localhost。真机和远程电脑使用带有效证书的 HTTPS 域名。

## 对外提供 HTTPS

将自己的域名指向服务端，开放 80/443。安装 Docker Compose 后：

```sh
export WUU_DOMAIN=wuu.example.com
export WUU_POSTGRES_PASSWORD="$(openssl rand -hex 32)"
docker compose -f deploy/remote/compose.yaml up --build -d
curl https://wuu.example.com/healthz
```

将域名和生成的密码保存到权限为 0600 的部署环境文件，每次 Compose 操作前加载；不要在升级时重新生成密码。示例把密码嵌入 URL，因此使用生成的十六进制密码；自选密码需处理 URL 编码。PostgreSQL 初始化变量只在空数据卷生效，修改环境变量不会修改已有数据库密码。

Compose 中的 Caddy 自动管理证书并代理 WebSocket。`accounts` 和 `postgres` 不直接发布端口。账号数据库位于持久卷 `postgres`，PostgreSQL 18 挂载目录为 `/var/lib/postgresql`；旧 QR 注册表位于 `accounts`，TLS 资料位于 `certificates`。Compose 项目名决定卷名前缀，升级时使用相同项目名。不要执行 `down -v`，它会删除持久数据。

注册默认由显式 `--registration` 开启。需要关闭公开注册时，从 Compose 的 `command` 中删除该参数并重建容器；保留可信代理参数，已有账号继续登录。不要把密码、令牌、恢复码写进代理访问日志。

后端默认按直连地址执行认证限速。Compose 为 Caddy 固定内部地址，并通过 `--trusted-proxies` 仅信任该地址的 `X-Forwarded-For`，让不同用户分别计数。自定义代理部署时，此参数接受逗号分隔的 CIDR；仅填写受控代理地址，代理必须覆盖或安全追加来源头，且后端应仅供代理访问。不要信任所有地址。若修改 Compose 子网，需同时调整 Caddy 地址和可信代理参数。

也可以在自己的 systemd、容器编排或其他反向代理后运行同一二进制。将进程绑定到内部地址，代理 `/v1/account/*`、`/v1/connect` 和 `/healthz`；WebSocket 必须支持长连接。账号服务依赖 PostgreSQL，也可连接外部托管 PostgreSQL；无需 Redis 或对象存储。

当前只支持一个账号/relay 服务实例。在线连接、配对窗口和撤销通知仍由该进程管理；不能把多个实例直接放在负载均衡后，即使它们共享 PostgreSQL。

## 电脑接入

需要后台系统提醒时，按 [自部署推送说明](PUSH.md) 配置自己的 APNs/FCM 凭据。账号和连接服务不依赖这些凭据。

在桌面 Wuu 左下角「本机使用」菜单选择「连接手机与电脑」，点击「已有服务，开始连接」。填写服务地址，确认连接成功后登录或创建账号，再在手机上连接同一服务并登录同一账号。登录会启动已有桌面远程宿主，复用当前桌面 Go 服务池。退出账号会停用本机远程访问。仅电脑开机、Wuu 运行且连接服务端时显示在线。

自动化或无界面电脑可以使用 `wuu remote account login` / `register`，JSON 从标准输入传入，字段为 `server`、`username`、`password`。密码不得出现在命令参数。再运行 `wuu remote host --workdir <工作目录>`；独立 CLI 宿主提供核心会话 RPC，桌面文件夹管理和终端等桌面能力需要桌面 App。

`wuu remote account status` 显示账号设备；`logout` 退出；`revoke` 从标准输入读取 `{ "pub": "设备公钥" }`。`password` 读取当前密码 `secret` 和新密码 `password`；`recover` 读取 `server`、`username`、恢复码 `secret`、新密码 `password`。

## 信任与数据保护

账号服务端是受信任的身份与设备目录。它看到用户名、密码派生值、恢复码散列、登录令牌散列、设备公钥/名称/角色、在线状态，以及通信时间和大小。启用推送时还保存设备的平台推送令牌；APNs/FCM 接收固定提醒文案和电脑公钥，不接收会话正文。密码通过 HTTPS 送达，使用 Argon2id（64 MiB、3 次迭代、2 路）和每账号随机盐保存。令牌为 256 位随机值，仅保存 SHA-256 散列，有效期 90 天。设备登录还需证明拥有其 Ed25519 私钥。

会话内容与工具执行留在电脑。中继转发通过认证的 X25519 临时握手和 AES-256-GCM 加密帧，不保存消息或附件。每次连接重新派生方向密钥，计数器拒绝重放。账号模式通过服务端目录分发设备身份，因此**恶意或被入侵的账号服务端可以授权攻击者设备**；此模式不能承诺抵御恶意身份服务器。原有 QR 配对的独立公钥固定信任流程仍可单独使用。

手机私钥和令牌分别保存到 iOS Keychain（仅本机、解锁后可读）与 Android Keystore 加密存储，不随 Android 系统备份复制。浏览器模式使用该来源的本地存储，不能用于共享或不受信任的 Web 来源。电脑的 `WUU_HOME/remote.json` 包含设备私钥与账号令牌，权限为 0600，需保护电脑用户账户和备份。服务端数据库凭据和 PostgreSQL 备份也包含敏感身份资料，应限制访问。

在设备列表移除丢失设备，会删除其身份和关联令牌，并断开当前账号连接；其他设备重新握手。改密码或用恢复码恢复会原子撤销账号全部设备并生成新的单次恢复码，需要在各设备重新登录。没有密码和恢复码时，服务器不能恢复原有密码；用户可新建账号并在仍持有的电脑上重新接入，电脑本地会话不会因此丢失。丢失电脑的本地磁盘内容需要操作系统磁盘加密保护。

## 备份、恢复与升级

升级前记录当前 Git 提交并备份 PostgreSQL 与旧 QR 注册表。`pg_dump` 生成一致的逻辑备份；下面停用账号进程，让两个存储对应同一停机窗口。不要在 PostgreSQL 运行时直接复制数据目录作为备份。

```sh
umask 077
mkdir remote-backup
docker compose -f deploy/remote/compose.yaml stop accounts
docker compose -f deploy/remote/compose.yaml exec -T postgres \
  pg_dump -U wuu -d wuu -Fc > remote-backup/accounts.dump
docker compose -f deploy/remote/compose.yaml cp accounts:/data remote-backup/relay
docker compose -f deploy/remote/compose.yaml start accounts
```

备份包含身份资料，使用自己的加密备份工具保存。恢复旧备份会恢复备份时仍有效的令牌和设备撤销状态；恢复后应重新检查丢失设备，必要时改密码撤销全部设备。

使用 SIGTERM 或 Ctrl-C 停止服务，会停止接收 HTTP 请求并关闭所有 WebSocket，再关闭账号数据库；客户端按已有恢复流程重连。

账号数据库不包含电脑会话。分别备份每台电脑的 `WUU_HOME` 和工作目录，恢复文件权限；不要将同一个 `remote.json` 同时复制到两台运行中的电脑，它们会共享设备身份并互相顶下线。

恢复 Compose 备份时，先恢复数据库，再恢复旧 QR 注册表。账号模式没有旧 QR 配对时，`relay.json` 可能尚不存在。

```sh
docker compose -f deploy/remote/compose.yaml stop accounts
docker compose -f deploy/remote/compose.yaml exec -T postgres \
  pg_restore -U wuu -d wuu --clean --if-exists --exit-on-error --single-transaction < remote-backup/accounts.dump
docker compose -f deploy/remote/compose.yaml cp remote-backup/relay/. accounts:/data
docker compose -f deploy/remote/compose.yaml run --rm --no-deps --user 0 \
  --entrypoint chown accounts -R 10001:10001 /data
docker compose -f deploy/remote/compose.yaml start accounts
```

启动时在事务及 PostgreSQL advisory lock 内初始化/迁移 schema，当前版本为 2；未知版本拒绝启动。升级前备份，停止旧进程后替换并启动。若未来版本迁移了 schema，回滚应用时同时恢复迁移前备份。PostgreSQL 大版本升级需要 `pg_upgrade` 或逻辑备份恢复，不能只改镜像大版本并复用旧卷。手机资源随本地 App 构建更新，不依赖服务端分发可执行代码。

## 验证自己的部署

在可丢弃的验证服务端开启注册后运行（Node.js 22+）：

```sh
node deploy/remote/verify.mjs create https://wuu.example.com /tmp/wuu-test-state.json
node deploy/remote/verify.mjs verify https://wuu.example.com /tmp/wuu-test-state.json
# 重启容器、备份并恢复后，再执行 verify，检查持久化。
node deploy/remote/verify.mjs revoke https://wuu.example.com /tmp/wuu-test-state.json
```

脚本创建两个随机测试账号，检查设备目录隔离、跨账号删除被拒绝、设备撤销和恢复码只能使用一次。测试状态文件包含测试令牌和恢复码，以 0600 保存且不覆盖已有文件；验证结束删除该文件。`revoke` 会改变测试账号状态；恢复之前的备份后可以再次 `verify`。本地自签 CA 通过 `NODE_EXTRA_CA_CERTS=/path/to/root.crt` 指定，不能禁用 TLS 校验。此脚本验证账号服务，手机与真实 Agent 的验证见手机 App 文档。
