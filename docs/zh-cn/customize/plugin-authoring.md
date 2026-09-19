# 编写插件

一个 Wuu 插件包可以组合 agent 运行时、桌面 UI、技能、hook、MCP 服务器、命令、主题和设置。完整示例见 [agent 快速上手](plugin-quickstart.md)或[桌面快速上手](desktop-plugin-quickstart.md)。本页说明这些示例使用的包和运行时契约。

## 包结构和 manifest

包根目录包含 `plugin.json`，只需声明实际使用的入口和贡献：

```json
{
  "schema_version": 1,
  "id": "example-plugin",
  "name": "Example Plugin",
  "version": "0.1.0",
  "runtime": {
    "protocol": "wuu-plugin-v1",
    "command": "node",
    "args": ["dist/runtime.js"]
  },
  "desktop": { "entry": "dist/renderer.js" }
}
```

这个例子需要两个已声明的文件。纯桌面或声明式插件可省略 `runtime`；不需要 renderer 代码时省略 `desktop`。桌面入口必须是包内相对路径指向的、自包含的 ESM 模块。打包会排除 `node_modules`，因此运行时依赖也必须在打包后可用；适用时应将 JavaScript 依赖一起打入运行时入口。

| 字段 | 契约 |
| --- | --- |
| `schema_version` | 包 schema，目前为 `1` |
| `id` | 稳定的包身份，更新时应保持不变 |
| `version` | 插件自身版本，与 Wuu 产品版本分开 |
| `minimum_wuu_version` | 最低 Wuu CalVer，例如 `2026.9.1`，不能代替服务或 API 兼容性检查 |
| `platforms` | 可选的宿主平台限制 |
| `requires` | 必须可用的插件包 ID；缺少依赖时，该插件不激活 |
| `breaks` | 硬不兼容关系；不兼容的包同时启用会拒绝激活计划 |
| `conflicts` | 软冲突，只报告，不自动禁用任何一方 |
| `skills`、`hooks`、`mcpServers` | 分别使用[技能](skill-authoring.md)、[hook](hooks.md)、[MCP](mcp.md) 契约的贡献 |
| `contributes` | 命令、主题、设置、UI 声明和视图入口 |

包关系填写 ID，不是版本范围，也不会自动下载依赖。依赖环会拒绝激活计划。`requested_permissions` 描述申请的权限，不会为插件进程建立沙箱。

## 声明式贡献

主题和设置不需要桌面模块。例如，将下面的片段合并到 manifest：

```json
{
  "contributes": {
    "themes": [{
      "id": "calm-night",
      "name": "Calm Night",
      "base": "dark",
      "tokens": {
        "--wuu-color-canvas": "#151820",
        "--wuu-color-accent": "#8fa7ff"
      }
    }],
    "settings": {
      "enabled": {
        "type": "boolean",
        "title": "Enable feature",
        "default": true,
        "scope": "workspace",
        "apply": "live"
      }
    }
  }
}
```

主题 token 来自[生成的主题契约](theme-surface-matrix.md)，不是任意宿主 CSS 变量。设置支持 `boolean`、`string`、`number`、`enum`，作用域为 `user|workspace`，应用方式为 `live|restart`。运行时通过 `host.settings.get/list` 服务读取，桌面 View 通过 `host.getSetting` 读取。禁用或删除包默认保留设置和存储。

`contributes.commands` 中的命令包含本地 ID 和 kind。`prompt_template` 引用包内有大小限制的 UTF-8 文件，提供宿主管理的输入框动作；`runtime_action` 指向桌面 generation 注册的命令，只有声明不会执行代码。

UI 声明放在 `contributes.slots`、`surfaces`、`presenters`、`navigation`、`workspaceTools` 或 `settingsPages` 中，描述放置位置和入口，实际渲染仍由桌面模块注册。匹配与组合规则见[桌面 UI 插件](desktop-plugins.md)。

## 运行时进程和生命周期

宿主启动 manifest 中的命令，通过 stdin/stdout 交换每行一个 JSON 对象。stdout 专用于协议帧，诊断信息写入 stderr。manifest 的传输名称 `wuu-plugin-v1` 与协商的能力协议编号是不同字段；使用服务的新运行时应协商 `protocol_version: 3`。

使用 `@wuu/plugin-sdk` 时，实现 `RuntimePlugin` 并传给 `runJSONLRuntime`。SDK 负责消息分帧、请求 ID、取消信号和 lifecycle-v1 握手。

| 回调 | 职责 |
| --- | --- |
| `initialize(params, host)` | 返回工具、能力、提供的服务和需要的服务 |
| `activate(host)` | 激活后启动定时器、订阅或其他行为 |
| `executeTool(params, host, execution)` | 执行已注册工具，返回结果 |
| `invokeCapability(params, host, execution)` | 处理已声明能力 |
| `invokeService(params, host, execution)` | 响应一次宿主路由的服务调用 |
| `serviceChanged(params, host)` | 响应服务解析变化 |
| `shutdown()` | 停止插件拥有的工作并释放资源 |

初始化是准备阶段，不代表可以开始产品行为。预检查期间，宿主只允许调用读取阶段的服务。写入和后台任务应推迟到激活后，shutdown 也应能安全处理只完成部分初始化的情况。generation 在成为活动版本前可能失败或被替换。

## 工具和能力

模型工具注册在初始化结果的 `tools` 数组中，每个工具需要 `id`、`description` 和对象类型的 `input_schema`，宿主会生成带命名空间的公开名称。`execution_scopes` 可限制工具在 `root`、`child` 或 `collaboration` 中可用；`activity` 描述只读性、并发安全、风险和是否编排子工具。应如实声明副作用，不要把写入工具标成只读来绕过调度或权限检查。

`executeTool` 接收参数以及 `cwd`、调用 ID、可用的会话和轮次标识等上下文。即使有 schema，也应验证参数。结果格式为 `{ result: { content: [...] } }`，工具失败时设置 `is_error: true`。内容可包含文本和支持的富结果部分。需要成为会话产物的文件应使用 `importArtifact`，不要只返回可能消失的临时路径。

能力单独声明 ID、`kind` 和版本。当前支持的能力 ID 为：

| 能力 | 类型和用途 |
| --- | --- |
| `agent.system_prompt.section` | `transform`：添加提示词区块 |
| `agent.request.transform` | `transform`：在模型请求前添加系统消息 |
| `agent.pre_step` | `transform`：在步骤前追加带标识的消息 |
| `agent.turn.completed`、`agent.turn.lifecycle`、`agent.turn.interrupted` | `observe`：响应支持的轮次事件 |
| `plugin.client.request` | `decision`：分派插件桌面 UI 发来的请求 |
| `agent.compaction` | `decision`：实验性的压缩契约 |

`agent.request.transform` 接收与 provider 无关的请求视图，公开输出是 `prepend_system_messages`，不是任意修改 provider 的传输请求。SDK 将压缩标为实验性，不应把它当作稳定的、可替换整个 agent 循环的通用接口。

能力可声明 `priority`、`depends_on` 和 `conflicts`。优先级高的先执行，同优先级保留发现顺序。`error_policy` 为 `propagate`、`isolate` 或 `ignore`；观察能力不能传播错误，只有观察能力能忽略错误。内置轮次观察能力默认隔离，其他能力默认传播。预期内的失败应在插件内部处理，不要只依赖统一错误策略。

## 调用宿主服务

当前宿主只通过 **`host.service.call` 运行时入口**路由服务。每项消费的服务都要在 `required_services` 中声明名称和主版本。旧的 `host.storage.get` 直接消息，以及在 `required_host_services` 中要求它的声明，都不是当前传输契约，即使 SDK 中仍保留类似兼容类型。

例如，下面的初始化结果申请读取存储：

```json
{
  "protocol_version": 3,
  "required_services": [
    { "name": "host.storage.get", "major_version": 1, "required": true }
  ]
}
```

激活后，通过统一入口调用：

```ts
import type { RuntimeHost } from "@wuu/plugin-sdk";

async function readCounter(host: RuntimeHost): Promise<string | null> {
  const result = await host.call("host.service.call", {
    service: "host.storage.get",
    method: "call",
    params: { scope: "workspace", key: "counter" },
  }) as { value: string | null };
  return result.value;
}
```

`requireKernelService` 可构造对应的版本 1 服务要求。`host.supports("host.service.call")` 只检查统一调用入口，不代表某个服务 provider 一定存在。可选服务也需要声明，并处理不可用时的错误。

| 服务族 | 用途 |
| --- | --- |
| `host.storage.get/set/delete/keys` | 插件用户或工作区命名空间中的字符串值 |
| `host.storage.compare-exchange` | 用 `{ scope, key, expected, value }` 条件替换，返回 `{ swapped, value }` |
| `host.settings.get/list` | 声明的设置值 |
| `host.session.create/send/list/inspect/cancel/control` | 按宿主生命周期规则创建和管理会话 |
| `host.session.history.read/search` | 使用序列快照和续读游标访问有限历史 |
| `host.workspace.status/apply/discard` | 检查或处理所拥有会话工作区中的改动 |
| `execution.update` | 报告活动执行的进展 |
| `execution.invoke-tool` | 在编排执行中调用子工具 |
| `host.artifact.import` | 将文件或字节导入为会话产物 |
| `host.user-question.ask` | 执行期间提出结构化问题 |

大多数核心服务使用 `call` 方法。存储不提供跨 key 事务；并发更新单个值时应使用 compare-exchange，检查 `swapped`，并限制重试次数。先读取再无条件写入会丢失并发更新。不存在的值为 `null`；结构化数据可编码为 JSON 等字符串存储。

创建会话时提供稳定的 `request_id`、`visibility=user|plugin` 和 `context_source=fresh|fork|seed`。发送也需要稳定的请求 ID 和 `input.prompt`。请求被接纳或排队不代表完成，应检查轮次或处理生命周期事件后再消费结果。模型提示、业务状态和重试策略由插件负责，执行、历史、工作区改动和恢复交给宿主。

## 提供服务

初始化时返回 `provided_services`，并实现 `invokeService`。描述包含小写点分名称、严格的 `MAJOR.MINOR.PATCH` 版本，以及带 `input_schema`、`output_schema` 标识的方法。这些标识命名契约，不是内嵌 JSON Schema 定义。

消费者声明服务名和主版本，再通过统一入口调用。宿主提供调用者身份并路由到活动 provider，不应直接寻址另一个插件进程或依赖其私有文件。同一初始化结果不能同时提供和消费同名服务。缺少必需服务会阻止消费者激活；同名同主版本的多个 provider 会产生诊断，不会合并实现。

## 执行与取消

工具和能力调用具有宿主管理的执行 ID。SDK 提供 `execution.signal`，应传给可取消操作，在信号中止时停止工作。调用返回后，执行作用域关闭；迟到的进度不能重新打开已完成执行，取消也不会等待插件确认。

`reportExecutionUpdate`、`invokeTool`、`askUserQuestions` 和 `importArtifact` 都调用版本化服务，需要对应声明。编排工具使用 `activity.orchestrator=true` 和 `execution.invoke-tool`，通过正常工具链委托副作用。重试相同工具和参数时，保持子调用 `call_id` 不变；不要用同一个 ID 表示不同工作。

`security.authorize` 服务可以进一步限制操作，但不能放宽宿主的强制边界。`sandbox.process` provider 必须提供真实的文件系统约束，不是命令审批弹窗。实现前请阅读[权限](../reference/permissions.md)。

## 开发和分发

`wuu plugin create` 生成 `agent`、`desktop` 或 `full` 包骨架。当前开发命令需要包含 `plugin.json` 和 `package.json` 构建脚本的目录。`wuu plugin dev` 重新构建并发布开发 generation，并不是无 manifest 的单 TypeScript 文件加载器。快速上手展示了如何使用匹配的本地 SDK，并打包必要的运行时代码。

用 `validate` 检查包结构，`test` 检查可执行初始化和协商描述，再通过真实会话或界面验证行为。`pack` 生成本地 zip，不会上传到 registry。分发前应检查压缩包内容：准备阶段排除 `.git` 和 `node_modules`，不会排除所有可能包含私人数据的本地文件。

当前 install/update CLI 会先暂存包，再单独批准代码激活。开发目录授权不会成为分发包的信任。命令见[插件管理](plugins.md)，信任边界见[安全模型](../reference/security-model.md)。

完整类型位于 [`packages/plugin-sdk/src/index.ts`](../../../packages/plugin-sdk/src/index.ts)，Go 接入使用 [`packages/plugin-go`](../../../packages/plugin-go/runtime.go)。请与目标 Wuu 版本匹配；产品 CalVer、包 schema、能力协议、服务版本和桌面快照版本是不同的兼容性检查。
