# Agent 命令系统

内置 Wuu 引擎通过 `bash` 执行命令、测试、构建和后台进程。命令在宿主机器上、当前会话的工作区中运行。外部引擎有自己的命令实现；本页介绍 Wuu 的工具。

## 运行命令

默认操作是 `action=run`。下一步依赖命令结果时，使用这个操作：

```json
{
  "command": "go test ./internal/process",
  "timeout_seconds": 300,
  "purpose": "Check process lifecycle behavior",
  "scope": "targeted"
}
```

结果包含退出码、耗时、有限长度的输出、输出尾部和工作区版本；可用时还会提供完整日志引用。被识别为测试、构建、lint 或类型检查的命令会附带验证元数据。`scope` 表示预期的检查范围（`targeted`、`affected` 或 `full`），不会改变命令实际运行哪些测试。

命令必须非交互执行，不能依赖编辑器、pager 或终端提示。默认等待 300 秒，可设置为 1–3600 秒。超时后，Wuu 通常会将仍在运行的进程交给后台管理器，并返回进程 ID。这**不代表验证成功，也不代表命令已经停止**。无法转交时，Wuu 会停止进程。取消当前调用的轮次也会停止前台命令，而不是将它转入后台。

## 后台和交互任务

启动服务、watcher 或交互命令时，使用 `action=start_background`，不要在命令后添加 `&`：

```json
{
  "action": "start_background",
  "command": "npm run dev",
  "completion_mode": "detached"
}
```

后台启动默认使用伪终端。纯日志自动化任务可设置 `tty=false`。macOS 和 Linux 支持 PTY；Windows 回退到管道，因此终端行为有所不同。

默认的 `completion_mode=resume` 会在进程自然退出后安排所属会话继续执行，也会让 `wuu exec` 等待这次续转。长期服务如果不应在退出时唤醒会话、也不应阻止当前任务结束，应使用 `detached`。

| 操作 | 用途 |
| --- | --- |
| `list_background` | 查看当前会话的受管进程 |
| `read_background` | 通过 `process_id` 读取输出；将上次的 `end_offset` 作为 `offset_bytes` 可增量读取 |
| `write_background` | 向仍有有效输入句柄的进程发送 `input`；需要时附上换行 |
| `stop_background` | 停止进程树 |
| `update_background` | 修改 `recheck_minutes`；设为 `0` 取消定时复查 |

读取默认最多返回 32 KiB。省略 `wait_ms` 时立即返回快照；指定后等待新输出或退出，最长 300,000 毫秒。首次启动可等待最长 60,000 毫秒。持续输出会按节奏返回，而不是每收到一个字节就结束等待。

长任务或安静的任务可设置 1–1440 分钟的 `recheck_minutes`，定时唤醒会话查看进展；完成后自动取消。不要连续等待来维持当前轮次：立即依赖结果时使用前台执行，否则让完成通知和定时复查唤醒会话。

## 目录、环境和权限

`cwd` 默认为工作区根目录，必须解析到已存在且允许访问的目录。绑定 worktree 的 worker 在分配给它的检出目录中执行。每次普通命令都会启动新 shell，`cd`、alias 和临时环境修改不会跨调用保留。

Wuu 通过 shell 启动器解析 Bash，Windows 使用 Git Bash。前台和后台命令共用环境准备逻辑：移除继承的构建期 `GOROOT`，设置非交互默认值；PTY 启动时再按需恢复终端相关设置。

命令分类用于权限检查和调度，不等于文件系统沙箱。在受限模式下，前台和后台进程都使用已配置的文件系统进程沙箱。如果没有可执行限制的 provider，启动会失败，不会静默改成不受限执行。当前内置 provider 仅支持 macOS；网络隔离不在此契约内。模式和平台限制见[权限](permissions.md)。

写入被拒绝后，不应换一条 shell 命令绕过限制。任务确实需要访问边界外的路径时，应添加已授权的工作区根目录，或显式切换会话模式。Wuu 没有逐条命令弹窗审批。

## 应用补丁后验证

已经确定后续命令时，可以让 `apply_patch` 在完整补丁应用成功后执行它：

```json
{
  "patchText": "*** Begin Patch\n*** Add File: example.txt\n+hello\n*** End Patch",
  "then_run": {
    "command": "test -f example.txt",
    "timeout_seconds": 30,
    "purpose": "Check the new file exists"
  }
}
```

`then_run` 接受 `command`、`cwd`、`timeout_seconds`、`purpose` 和 `scope`，沿用正常命令的权限检查和记录。它不能与 `dry_run` 同时使用。补丁失败会跳过命令；命令失败则保留已经成功的补丁。验证转入后台后，必须等到实际终态结果才能判断是否通过，不要为了重跑验证而重新应用补丁。

每个解析后的路径只能属于一个文件段，移动的源路径和目标路径也计入检查。`a.txt` 与 `./a.txt` 等别名视为同一路径。同一文件的多处修改应合并到一个 `Update File` 段的多个 `@@` 块中。路径冲突会在任何写入或 `then_run` 前验证失败，`dry_run` 也遵循这个规则。

## 日志与存续时间

普通命令输出和保存的完整工具日志会先脱敏，再返回或持久化。后台进程记录以及合并的 stdout/stderr 日志在磁盘上可能包含原始文本，工具返回时才脱敏。不要把密钥放入参数或输出，分享日志前应自行检查。

运行中的后台日志可能持续增长。结束后的日志会压缩为最多 8 MiB 的尾部；启动维护会清理已结束超过 30 天且没有待交付完成通知的记录和日志。因此，之前的字节偏移可能早于当前保留的输出，应使用结果返回的偏移，不要假设完整日志始终可读。前台命令执行期间也会把输出缓存在内存中，异常高输出可能占用大量内存。

默认的 `lifecycle=session` 会参与运行时清理，`managed` 会跳过这类清理。两者都不是操作系统服务管理器。Wuu 重启后无法恢复丢失的 PTY 或 stdin 句柄，重新发现的进程也可能没有可靠退出码。不再需要进程时应显式停止，不要依赖会话标签页的存续时间。停止前，Wuu 会检查进程身份，避免误操作已被复用的 PID，然后先请求温和退出，必要时再终止进程树。

桌面进程控件和模型工具使用同一个管理器。app-server 的进程方法会检查 thread 归属；客户端不能仅凭猜到进程 ID 就操作其他 thread 的进程。接入说明见 [app-server 协议](../../en/integrations/app-server-protocol.md)。
