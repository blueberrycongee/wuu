# 外部 Agent 引擎

外部引擎运行已安装的 Agent，使用它自己的账号、模型配置、工具和原生会话存储。它与内置 Wuu 引擎使用的[模型服务](model-services.md)不同。除了 Codex 和 Claude Code，Wuu 还提供下列七种接入。

## 安装与选择引擎

请按上游说明自行安装 Agent 或适配器，Wuu 不会下载可执行文件。在设置的引擎区域查看检测结果、指定可执行文件路径或禁用引擎，然后在输入框中选择可用引擎，也可以设置新会话的默认引擎。

| 引擎 ID | 启动命令 | 接入方式与安装说明 |
|---|---|---|
| `cursor` | `cursor-agent acp` | ACP v1；[Cursor CLI](https://cursor.com/docs/cli/acp) |
| `devin` | `devin acp` | ACP v1；[Devin CLI](https://docs.devin.ai/cli) |
| `grok` | `grok --no-auto-update agent --no-leader stdio` | ACP v1；[Grok CLI](https://x.ai/cli) |
| `hermes` | `hermes acp` | ACP v1；[Hermes ACP](https://hermes-agent.nousresearch.com/docs/user-guide/features/acp) |
| `pi` | `pi-acp` | 通过[社区适配器](https://github.com/svkozak/pi-acp)使用 ACP v1，不是直接运行 `pi` |
| `opencode` | `opencode serve --hostname 127.0.0.1 --port 0` | 原生 HTTP/SSE，要求 OpenCode 1.x；[OpenCode](https://opencode.ai/docs) |
| `antigravity` | `agy_acp_server` | ACP v1；需另行安装兼容服务端；[Antigravity 扩展](https://antigravity.google/docs/ide/extensions) |

Antigravity 也会检测 `agy_acp_server.par`；Linux 启动时附加 `--uid=`。这些命令描述适配器要求，不代表每个 CLI 版本都兼容。ACP 必须协商为协议版本 1；OpenCode 必须报告健康的 1.x 服务。不兼容时会明确报错。

这七种引擎依次使用 `engines.<id>.binary_path`、`WUU_<ID>_BINARY`（如 `WUU_DEVIN_BINARY`）、`PATH` 查找可执行文件。指定路径无效时直接报错，不会悄悄换用其他程序。路径应指向程序，不要填写带参数的命令。检测只查找可执行文件，不验证凭据，也不创建对话。

机器本地配置为每个引擎提供 `enabled`、`binary_path`，并用 `default_engine` 指定新会话默认值。省略 `enabled` 表示自动检测，`false` 表示禁用。例如：

```json
{
  "engines": {
    "default_engine": "devin",
    "devin": { "enabled": true },
    "grok": { "enabled": false }
  }
}
```

缺失或禁用的引擎无法运行。已有会话保留原引擎绑定，不会自动切换。配置文件位置与作用范围见[配置参考](../reference/configuration.md)。

## 登录与模型选择

可以使用 Agent 自身的 CLI 登录和配置。ACP 引擎也在设置中提供**登录**入口：点击后才启动 Agent 并查询登录方式，明确选择一种方式后才调用认证流程，该流程可能打开浏览器。打开设置、展开引擎或刷新检测不会发起这一流程。取消会停止登录进程；凭据仍由 Agent 自己保存。

此界面只支持 Agent 驱动的认证方式，没有可选方式时请使用原生 CLI。查询方式不是检查账号状态，登录成功也不保证模型可用。Wuu 不会把这些引擎的凭据导入模型服务。

ACP 引擎如果在 `session/new` 中声明了模型，输入框会列出这些模型。Grok 使用其一等模型列表（`grok-4.6`、`grok-4.5` 等）并通过 `session/set_model` 切换；推理强度在 Agent 声明 `thought_level` 时可选。Agent 未声明模型时仍显示 **Agent 默认模型**。Wuu 不会套用自己的模型服务目录。通过 API 指定 ACP 模型时，必须使用 Agent 声明的模型；OpenCode 模型 ID 使用 `provider/model` 格式。图片输入和宿主 HTTP MCP 工具依赖原生能力；ACP 不支持所需能力时会报错，不会丢弃输入或工具。

## 权限与会话恢复

这些适配器以你的用户权限启动受信任的本地程序。Wuu 不会把它们的原生工具放入自己的操作系统进程沙箱，也无法强制只读边界，因此会在启动前拒绝 `read_only` 轮次。在标准模式中，协议收到的权限请求进入 Wuu 审批流程；没有审批支持时拒绝请求。`unconfined` 接受受支持的单次请求。ACP 审批不会转换成持久的 `allow_always` 规则。OpenCode 每轮都会刷新 `ask` 规则，恢复会话时也一样。Agent 不发起权限请求就执行的行为不在这一审批边界内。新外部引擎会话在调用方省略权限模式时默认使用 `unconfined`，请明确选择。详见[权限说明](../reference/permissions.md)。

Wuu 保存原生会话引用，并在后续轮次加载。如果 ACP Agent 不支持加载会话，或原会话加载失败，本轮会报错，不会静默创建新历史。一轮通常由原生提示请求的响应结束。Grok 还可能先发出 `x.ai/session/prompt_complete` 扩展通知；当这条通知先到时，Wuu 用它结束本轮，因为 Grok 的提示 RPC 可能在回合实际结束后仍挂起。不以一段时间没有输出作为完成依据；Grok 若始终不确认提示，会报错而不是一直等待。停止操作会取消原生任务并清理子进程。OpenCode 使用带密码的本机回环服务，每个进程生成新密码，不作为远程服务开放。
