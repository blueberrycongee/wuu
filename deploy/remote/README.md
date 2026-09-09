# 自部署 Wuu 账号与连接服务

服务端、手机客户端和桌面接入代码都在本仓库。无需 Wuu 官方账号、云服务、开发者密钥或邮件服务。电脑仍是 Agent 的执行与会话持久化来源；电脑离线时，服务端不会代执行或唤醒电脑。

## 本地启动

安装仓库要求的 Go 1.26.5，运行：

```sh
go build -trimpath -o ./bin/wuu ./cmd/wuu
mkdir -p ./private-remote-data
chmod 700 ./private-remote-data
./bin/wuu relay --addr 127.0.0.1:8787 \
  --state ./private-remote-data/relay.json \
  --accounts ./private-remote-data/accounts.db --registration
```

`GET /healthz` 返回 `ok`。从电脑和手机的账号页选择此服务端、创建账号，然后在另一端登录同一账号。用户名为 3–64 个小写字母、数字、点、连字符或下划线；密码至少 12 字节。请保存注册时显示的恢复码。

`http://127.0.0.1` 只适用于本机测试。Android 模拟器通过 `adb reverse tcp:8787 tcp:8787` 访问电脑的 localhost；iOS 模拟器直接访问电脑 localhost。真机和远程电脑使用带有效证书的 HTTPS 域名。

## 对外提供 HTTPS

将自己的域名指向服务端，开放 80/443。安装 Docker Compose 后：

```sh
export WUU_DOMAIN=wuu.example.com
docker compose -f deploy/remote/compose.yaml up --build -d
curl https://wuu.example.com/healthz
```

Compose 中的 Caddy 自动管理证书并代理 WebSocket。`accounts` 不直接发布端口。账号数据库位于持久卷 `accounts`，TLS 资料位于 `certificates`。Compose 项目名决定卷名前缀，升级时使用相同项目名。不要执行 `down -v`，它会删除持久数据。

注册默认由显式 `--registration` 开启。需要关闭公开注册时，将 Compose 的 `command` 改成 `[]` 并重建容器；已有账号继续登录。不要把密码、令牌、恢复码写进代理访问日志。后端不信任 `X-Forwarded-For`，认证限速按直连地址计算；多用户代理部署应在代理层增加来源 IP 限速并保留后端保护。

也可以在自己的 systemd、容器编排或其他反向代理后运行同一二进制。将进程绑定到内部地址，代理 `/v1/account/*`、`/v1/connect` 和 `/healthz`；WebSocket 必须支持长连接。无需 Redis、Postgres、对象存储或任何开发者基础设施。

## 电脑接入

在桌面 Wuu「设置 → 手机访问」填写服务端并登录。登录会启动已有桌面远程宿主，复用当前桌面 Go 服务池。退出账号会停用本机远程访问。仅电脑开机、Wuu 运行且连接服务端时显示在线。

自动化或无界面电脑可以使用 `wuu remote account login` / `register`，JSON 从标准输入传入，字段为 `server`、`username`、`password`。密码不得出现在命令参数。再运行 `wuu remote host --workdir <工作目录>`；独立 CLI 宿主提供核心会话 RPC，桌面文件夹管理和终端等桌面能力需要桌面 App。

`wuu remote account status` 显示账号设备；`logout` 退出；`revoke` 从标准输入读取 `{ "pub": "设备公钥" }`。`password` 读取当前密码 `secret` 和新密码 `password`；`recover` 读取 `server`、`username`、恢复码 `secret`、新密码 `password`。

## 信任与数据保护

账号服务端是受信任的身份与设备目录。它看到用户名、密码派生值、恢复码散列、登录令牌散列、设备公钥/名称/角色、在线状态，以及通信时间和大小。密码通过 HTTPS 送达，使用 Argon2id（64 MiB、3 次迭代、2 路）和每账号随机盐保存。令牌为 256 位随机值，仅保存 SHA-256 散列，有效期 90 天。设备登录还需证明拥有其 Ed25519 私钥。

会话内容与工具执行留在电脑。中继转发通过认证的 X25519 临时握手和 AES-256-GCM 加密帧，不保存消息或附件。每次连接重新派生方向密钥，计数器拒绝重放。账号模式通过服务端目录分发设备身份，因此**恶意或被入侵的账号服务端可以授权攻击者设备**；此模式不能承诺抵御恶意身份服务器。原有 QR 配对的独立公钥固定信任流程仍可单独使用。

手机私钥和令牌分别保存到 iOS Keychain（仅本机、解锁后可读）与 Android Keystore 加密存储，不随 Android 系统备份复制。浏览器模式使用该来源的本地存储，不能用于共享或不受信任的 Web 来源。电脑的 `WUU_HOME/remote.json` 包含设备私钥与账号令牌，权限为 0600，需保护电脑用户账户和备份。服务端账号数据库也是 0600。

在设备列表移除丢失设备，会删除其身份和关联令牌，并断开当前账号连接；其他设备重新握手。改密码或用恢复码恢复会原子撤销账号全部设备并生成新的单次恢复码，需要在各设备重新登录。没有密码和恢复码时，服务器不能恢复原有密码；用户可新建账号并在仍持有的电脑上重新接入，电脑本地会话不会因此丢失。丢失电脑的本地磁盘内容需要操作系统磁盘加密保护。

## 备份、恢复与升级

备份前停止账号进程或容器，再备份 `/data`。原生部署备份 `accounts.db` 和 `relay.json`；Compose 示例：

```sh
docker compose -f deploy/remote/compose.yaml stop accounts
docker compose -f deploy/remote/compose.yaml cp accounts:/data ./remote-backup
docker compose -f deploy/remote/compose.yaml start accounts
```

备份包含身份资料，使用自己的加密备份工具保存。恢复时停止服务，把整份 `/data` 恢复到相同位置、确保容器 UID 10001 有读写权限，再启动。恢复旧备份会恢复备份时仍有效的令牌和设备撤销状态；恢复后应重新检查丢失设备，必要时改密码撤销全部设备。

账号数据库不包含电脑会话。分别备份每台电脑的 `WUU_HOME` 和工作目录，恢复文件权限；不要将同一个 `remote.json` 同时复制到两台运行中的电脑，它们会共享设备身份并互相顶下线。

升级前备份，记录正在运行的 Git 提交，构建新的二进制/镜像，停止旧进程后替换并启动。数据库当前 schema 为 1；未知版本拒绝打开。若未来版本迁移了 schema，回滚应用时同时恢复迁移前备份。手机资源随本地 App 构建更新，不依赖服务端分发可执行代码。
