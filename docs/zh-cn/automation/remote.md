# 远程 Host 与 Relay

远程控制通过 Relay 将已配对客户端连接到 Wuu Host。Host 运行 Agent 并访问工作区，
Relay 转发端到端加密的 app-server 连接。配对授予的是 Host 控制权，不只是只读查看权限。

当前桌面发布流程隐藏账号和远程控制界面。本页说明源码 CLI 与开发客户端，不代表正式
桌面版已提供手机连接设置页。CLI 构建方法见[开发指南](../project/development.md)。

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
