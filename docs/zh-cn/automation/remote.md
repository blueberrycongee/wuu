# 远程 Host 与 Relay

远程控制通过 Relay 将已配对客户端连接到 Wuu Host。Host 运行 Agent 并访问工作区，
Relay 转发端到端加密的 app-server 连接。配对授予的是 Host 控制权，不只是只读查看权限。

手机配对目前是开发预览：用 `npm --prefix desktop run dev:remote` 启动桌面，
可在「设置 → 手机访问」中直接配对原生 iOS App，无需账号。发布验收完成前，
正式构建暂不展示手机访问入口。App 目前需要从
[原生客户端项目](../../../clients/native/README.md)构建，尚未提供公开 App Store 分发。
桌面账号登录仍仅在开发模式开放。CLI 构建方法见[开发指南](../project/development.md)。

## 连接 SSH 工作区

实验性的无桌面宿主在远端 macOS 或 Linux 电脑上运行 Wuu、已安装的 Agent 引擎、
文件、Git 和终端，本地浏览器通过 OpenSSH 隧道显示共享 Wuu 工作台。
Codex、Claude Code、Pi、OpenCode 等受支持引擎需在**远端**安装并登录；
引擎发现与会话状态都属于该电脑。

在远端同一份 Wuu 源码中构建，需要 Node 22+ 和 `go.mod` 指定的 Go 版本：

```bash
make setup
npm --prefix desktop run build:remote
npm --prefix desktop run remote:serve -- --workdir /srv/my-project --port 8787 --pair
```

用 tmux 或服务管理器运行最后一条命令，避免退出 SSH 时服务随之停止。
服务只监听 `127.0.0.1`，并打印一次性的浏览器配对链接。
构建产物包含匹配的 Go 核心和网页资源；也可用 `--core` 和 `--web-root`
指定路径。不要让桌面的远程 Host 与该服务同时使用同一个 Wuu home。

SSH 服务使用本地设备配对；如果其 Wuu home 已登录 Wuu 账号，服务会拒绝启动，
并保留现有账号凭据。可使用独立目录，例如
`WUU_HOME="$HOME/.wuu-ssh" npm --prefix desktop run remote:serve -- --workdir /srv/my-project --pair`，
或自行退出该 Wuu 账号后再启动。独立目录拥有自己的 Wuu 配置和会话历史，
请在开始任务前完成配置。

在本地从同一版本构建、安装 CLI，然后连接：

```bash
wuu remote ssh --port 8787 user@server
```

打开远端服务打印的配对链接。以后访问 `http://127.0.0.1:8787/`
即可使用已保存的配对。使用期间保持 SSH 命令运行，Ctrl-C 关闭隧道。
SSH 配置别名、密钥、主机密钥校验和 ProxyJump 沿用本机 OpenSSH 配置；
可用 `--ssh-port` 或 `--ssh-config` 显式指定连接参数。Wuu 始终在前台独占这条隧道，
不会为目的地主机连接复用或留下 control master，即使本机配置默认启用了连接共享或
后台运行。跳板机仍遵循其自身的 SSH 配置；已有或显式后台运行的跳板机 master 由用户管理。

Linux 和 macOS 上，没有控制终端时，Wuu 会将 SSH 及其前台代理子进程放在独立进程组中，
取消连接也会清理仍在连接的跳板机进程。有交互终端时保留 shell 作业控制和 OpenSSH
提示，即使标准输入被重定向也可交互认证。请用 Ctrl-C 向整个前台作业发送信号。
在这种交互场景下，只向 Wuu 的 PID 发信号，不能保证清理仍在认证的代理进程；
服务自动化应在没有控制终端的环境运行，或停止整个进程组。

CLI 的连接提示不是应用健康检查。请等待远端服务打印 `Wuu workspace ready` 后再
打开工作台；这表示监听器已运行，Host 已完成注册。打开工作区时才会发现远端的
Agent 引擎。`ExitOnForwardFailure` 能发现转发配置失败，不能证明远端应用可用。
如果 SSH 报错退出，请按报错排查后重新运行同一命令。Wuu 不会自动重试 SSH 或
反复请求密码；隧道恢复后，浏览器会自动重连。

每台主机使用不同的本地端口，让浏览器身份和草稿保持独立。
例如将远端 8787 映射到本地 8788：远端启动参数使用
`--port 8787 --browser-port 8788 --pair`，本地运行
`wuu remote ssh --port 8788 --remote-port 8787 user@server`。
更换对外声明的浏览器端口后需要重新配对。

关闭浏览器或断开 SSH 后，Agent 任务仍在远端服务中运行。重新连接隧道后，
工作台恢复会话与缓存输出。恢复原连接时终端也继续保留；若建立了全新连接，
工作台会提示重新打开终端。停止或重启远端服务会停止任务和终端，
已保存的会话历史仍在磁盘上。重启时不加 `--pair`，即可接受已有设备，
同时不开启新设备配对。

如果启动时的工作区被移除或迁移，重连仍可通过共享的「对话」工作区发现项目，
然后重新选择需要的项目。

### 将 Host 作为服务运行

Linux 使用 systemd 时，先按上面的命令交互配对，然后停止该 Host，再启动服务。
通过 `command -v node` 确认 Node 的绝对路径，并替换实际源码、工作区和 Wuu home 路径：

```ini
# ~/.config/systemd/user/wuu-workspace.service
[Unit]
Description=Wuu SSH workspace

[Service]
Type=simple
WorkingDirectory=/srv/wuu
Environment=WUU_HOME=/home/YOUR_USER/.wuu-ssh
ExecStart=/usr/bin/node /srv/wuu/desktop/out-remote/host.mjs --workdir /srv/my-project --port 8787
Restart=on-failure
RestartSec=5
KillMode=control-group
TimeoutStopSec=15
UMask=0077

[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now wuu-workspace
journalctl --user -u wuu-workspace -f
# 主动停止服务及其任务：
systemctl --user stop wuu-workspace
```

如果管理员未启用用户 lingering（`loginctl enable-linger YOUR_USER`），用户服务可能在
登出时停止。请遵循电脑的管理策略；Wuu 不会自行安装或启用服务。macOS 可用 tmux，
或使用相同 Node 命令的 launchd 服务。服务重启会恢复已保存的历史，但不是进程检查点：
运行中的回合与终端会停止。启动失败或收到 SIGINT/SIGTERM/SIGHUP 时，服务会清理其
启动的子进程；取消后不会再报告就绪。
不要在自动重启的服务中加入 `--pair`；仅在准备配对新浏览器时开启配对，
并避免把配对链接写入共享日志。

这一生命周期参考了 [code-server](https://coder.com/docs/code-server/guide#port-forwarding-via-ssh)
的预安装服务与回环 SSH 转发方式。OpenSSH 负责[端口转发](https://man.openbsd.org/ssh_config#ExitOnForwardFailure)
和[连接共享](https://man.openbsd.org/ssh_config#ControlMaster)；Wuu 的已有会话连续性机制负责恢复连接。

当前是从源码构建的浏览器工作台预览。无桌面宿主不提供原生桌面窗口、
内嵌浏览器/电脑控制工具或自动安装远端软件。需要原生浏览器的引擎登录，
应先在远端完成。本机的 SSH 凭据和模型配置不会上传。

## 连接 iOS App

1. 保持电脑上的 Wuu 运行。在「设置 → 手机访问」中允许访问，显示配对码。
2. 手机连接同一 Wi-Fi，或按下文配置双方可达的 Tailscale 地址或 Relay。在 App 的
   「连接电脑」中扫描配对码，或粘贴链接后选择「连接」。
3. 选择已有会话，或新建会话并选择工作目录；也可以输入电脑上已经存在的绝对目录路径。
   消息和任务控制通过加密连接发送给该电脑。

首次发送前，打开「**… → 会话设置**」选择引擎，列表来自所连接电脑上已启用且可用的引擎。
已有会话会显示绑定的引擎；使用其他引擎需要新建会话。同一页面提供所选引擎的模型、推理配置
和主机声明的权限选项，修改需要电脑在线且任务空闲。iOS 会记住保存的模型、推理配置与
执行权限，包括确认后的「不受限」，用于同一电脑、同一引擎的新会话，重启后仍有效；
账号连接的偏好按账号隔离。已有会话保留自己的设置。已移除的模型回退到电脑默认设置，
引擎仍支持的权限选择会继续保留。引擎安装、登录与凭据仍在电脑上管理。

App 将固定的电脑身份和手机密钥保存在 iOS 钥匙串中。重启后恢复上次的电脑、目录与会话；
从后台返回时建立新的加密连接，不会自动重发已经发送的消息。新建、发送和停止任务要求电脑
在线。账号历史同步和通知需要使用账号连接。

回复进行中发送的后续消息，会排在输入栏上方的紧凑队列中。点击预览可读全文、移除消息，
或立即引导当前回复；尚未消费的引导也可以改回排队。电脑开始处理消息后，它会从队列移除。
上滑可自动读取更早的消息，点击操作摘要可查看详情。最终回复出现后，前面的过程文字和
工具调用会收成摘要。读取、查看图片的结果默认收在操作详情中，展开后才加载；
明确展示给用户的图片仍保留在消息流中。已发布图片的缩略图和原图按需通过加密连接读取，
打开原图时校验已保存的内容摘要。

原生 iOS 和 Android App 支持离线渲染 Mermaid 代码块，并随回复逐步更新。
流式语法暂时不完整时保留上一张有效图，结束后语法仍有误则显示源码。
可点击「查看源码」或「复制代码」，宽图与高图可在图表内滚动；图表颜色和字号跟随手机外观设置。

长按自己的消息气泡或附件，可复制、编辑、选择文本或分享。回复的复制和分叉
按钮会在本轮结束后显示。分叉时可选择当前目录，或基于当前提交创建新的 Git 工作树；
创建工作树要求当前目录属于 Git 仓库。新会话先加载近期历史，上滑可继续读取更早的消息。
编辑会保留原附件，
重新发送时替换这条消息及之后的对话；取消不会修改历史。编辑需要电脑在线、会话可写且空闲，
并已处理完待发消息。

使用 Tailscale 或反向代理时，给桌面进程设置 `WUU_WEB_URL` 为手机可达的 HTTP(S) origin，
并将 `WUU_WEB_LISTEN` 设置为本机监听地址（例如 Tailscale 地址和端口）。使用独立 Relay 时，
还需将 `WUU_WEB_RELAY_URL` 设置为它的 `wss://…/v1/connect` 地址。配对码会携带该地址，
双方都必须能够访问；安装 Tailscale 本身不会修改已经保存的 Relay 地址。更改对外地址后，
应重新生成配对码并配对。

在手机上移除电脑只会删除本机保存的身份。撤销手机的访问权限，需要在电脑的「手机访问」
设置中移除该设备。

## 准备 Relay

Host 和客户端需要能访问同一个以 `/v1/connect` 结尾的 WebSocket 地址。单机开发可运行：

```bash
wuu relay --addr 127.0.0.1:8787
```

保持进程运行，使用 `ws://127.0.0.1:8787/v1/connect`。另一台设备不能通过它自己的回环
地址访问这台电脑。跨设备时，应部署双方都能访问的 Relay，并使用带 TLS 的 `wss://`。
服务支持 TLS 证书和私钥选项，也支持反向代理部署；消息内容加密不免除对路由元数据、
服务可用性和运维访问的保护。

账号服务和浏览器托管是需要单独配置的可选服务端能力。下面的基础 CLI 配对流程不要求
注册账号。服务端选项见
[`internal/remote/server/server.go`](../../../internal/remote/server/server.go)。

## 启动并配对 Host

在工作区所在电脑上运行：

```bash
wuu remote init --relay ws://127.0.0.1:8787/v1/connect --name my-mac
wuu remote host --workdir /path/to/project --pair
```

初始化会创建或更新 Host 身份并打印指纹。Host 主动连接 Relay，注册后打印配对 URI。
配对窗口默认持续 10 分钟，第一次成功配对后关闭。仅在需要时使用 `--pair-timeout 30m`
或 `--pair-once=false`。

`remote host` 接受 `--provider`、`--model`、`--workspace-id` 和 `--relay` 覆盖项。
Host 必须持续运行。同一个 Wuu home 只能由一个远程 Host 占用，启动另一个前应先关闭
已有 Host。

在开发客户端私下导入 URI：

```bash
wuu remote phone pair --uri 'wuu://pair?...' --name dev-client
wuu remote phone status
wuu remote phone send "总结当前工作区"
wuu remote phone send --thread THREAD_ID "继续调查"
wuu remote phone watch
```

`phone` 是 CLI 开发客户端，也可以在另一台电脑上运行。`send` 默认创建新会话，提供
`--thread` 则继续指定会话，然后流式输出回合内容。`watch` 显示连接状态和缩略通知。
这些输出不遵循 [`wuu exec` JSONL](../../en/automation/jsonl-events.md)（英文）的协议。

## 状态与设备移除

Host 身份和配对设备保存在 `WUU_HOME/remote.json`，客户端身份保存在
`WUU_HOME/phone.json`，默认 home 是 `~/.wuu`。phone 子命令的 `--store FILE` 可以选择
另一份客户端身份。上述文件和配对 URI 包含敏感访问材料，不要附在问题报告中。

```bash
wuu remote status --json
wuu remote devices --json
wuu remote devices remove DEVICE_FINGERPRINT
```

移除操作更新磁盘上的设备列表，不会更新运行中 Host 的内存状态。可靠撤销的顺序是先
停止 Host，再移除设备，最后在不开启配对窗口的情况下重启。不能认为执行移除就会断开
已有客户端。

## 排查连接

配对失败时，检查双方是否使用同一个可达 Relay，以及配对窗口是否仍有效。
`remote status` 中存在已保存身份不代表 Host 在线，应结合运行中的 Host 日志和客户端
连接状态判断。

客户端断线本身不会停止 Host 上的 Agent。重新连接后先检查原会话，再决定是否重发任务。
事件重放有容量限制，不能当作无限的持久日志存储。

已配对客户端使用 Host 的 app-server 控制接口。应按控制端信任它，包括它请求修改配置
的能力。Agent 操作仍受当前生效的[权限模式](../reference/permissions.md)约束；配对不是
独立的逐设备只读策略。开发自定义客户端可参考 [app-server 集成](app-server.md)。
