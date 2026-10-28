# 执行环境

在 **设置 → 内置 Agent → 执行环境** 中，可以让工作区工具运行在容器或远程机器上。默认仍在本机执行。环境内应安装与宿主相同版本的 Wuu；协议不匹配时会停止执行。

在**设置 → 内置 Agent → 执行环境**中创建配置，并在同一区域将其选为新会话的默认环境。会话创建时会保存环境选择和配置；修改或删除配置不会迁移已有会话。旧会话继续在本机执行。分叉会话继承配置，默认使用独立环境，启用共享时才共用文件系统。原地执行的子代理和只读侧聊使用父会话的环境。

文件、搜索、命令、Git、程序化工具调用及后台进程都进入该环境。桌面可以接收进程事件、查看输出和停止进程。展示产物会将不可变的文件快照传回宿主，单个文件上限为 256 MiB。模型连接、插件、浏览器集成、笔记和历史仍在宿主运行。此设置不会隔离扩展。

## 从 Docker 开始

在 Wuu 源码目录构建内置镜像：

```sh
docker build -f containers/execution/Dockerfile -t wuu-execution:local .
```

创建 Docker 配置，镜像填写 `wuu-execution:local`，工作目录填写 `/workspace`。镜像包含 Wuu、Git、ripgrep、Python 和 Node.js。项目所需的其他依赖可以添加到派生镜像。Docker 服务需要预先启动。

工作目录初始为空，可以在环境中克隆仓库，也可以显式挂载本机目录。使用同一挂载目录的会话会共享其中的文件，即使容器彼此独立。只读挂载可以阻止容器修改该目录。Linux 上挂载目录默认使用宿主用户和用户组，以保留文件归属；可通过容器用户字段覆盖。本机项目与执行环境不会自动同步。桌面编辑器、终端和 Git 面板仍对应本机项目；请通过代理的文件、Git 和进程工具查看执行环境，通过展示产物预览结果。

## 后端

| 后端 | 前置条件 | 资源与网络 |
| --- | --- | --- |
| Docker | 已启动的 Docker 服务及包含执行程序的镜像 | CPU、内存限制及可选的网络隔离 |
| SSH | 可连接的 Unix 主机、Wuu 程序、SSH 身份及已验证的主机密钥 | 资源和网络限制由远程服务器配置 |
| Singularity / Apptainer | 已安装的运行时及包含执行程序的镜像 | 使用运行时的 CPU、内存及网络命名空间选项，需要宿主允许 |
| Modal | Python 3.10+、`modal` 1.5+、已配置 SDK 凭据及可访问的执行镜像 | CPU、内存限制及网络隔离 |
| Daytona | Python 3.10+、`daytona` SDK、已配置凭据及可访问的执行镜像 | 整数 CPU 核数、向上取整至 GiB 的内存及网络隔离 |
| Vercel Sandbox | Python 3.10+、已登录的 `sandbox` CLI 3+ 及位于服务镜像仓库中的执行镜像 | 整数 CPU 核数、随 CPU 分配的内存及网络隔离 |
| Command | 已安装且可信、实现带版本执行后端协议的适配器 | 由适配器负责创建环境及落实限制 |

云端依赖是可选的，不会自动安装。Python 字段可以指向安装了 SDK 的虚拟环境。通过服务提供方的 SDK 或 CLI 登录流程配置凭据。宿主适配器使用这些凭据创建环境，不会自动将其交给工作区命令；环境自身的镜像或服务器可能已有凭据。

SSH 可填写主机或 SSH 别名、端口、身份文件和已知主机文件。必须严格校验主机密钥，请先通过常规 SSH 流程连接并验证。Wuu 会在指定的远程工作目录下创建会话专属子目录，远程账号需要相应写权限。程序化工具调用需要 Node.js 22.19+，Git 和搜索工具分别需要 Git 和 ripgrep。

Linux 命令写入限制需要 Landlock ABI 3 或更新版本。若不可用，标准或只读模式的命令会报错，不会自动放弃限制。参见[权限](../reference/permissions.md)。容器或虚拟机隔离与会话权限模式分别生效。

## 共享、保留与恢复

默认每个会话具有独立环境标识。**在会话间共享** 会让文件系统共用，但工具状态和进程归属仍按会话区分。显式挂载的本机目录始终共享，不受此开关影响。

**保留环境文件** 可以跨正常断开保留 Docker 容器、Singularity 工作目录、SSH 工作目录或云端文件系统。Modal 创建文件系统快照，Daytona 停止环境，Vercel 使用持久化沙箱。共享环境会保留供其他会话使用。保留文件不保证进程能跨云端停止、服务超时或机器重启继续运行。已保留的状态丢失时会报错，不会悄悄换成空文件系统。

未启用保留时，Docker 容器在运行时关闭时移除；Singularity 状态在连接关闭后删除；SSH 工作目录在工作进程断开并达到空闲超时后删除；云端环境在正常断开时停止或删除。宿主异常退出可能遗留资源。保留、共享的云端环境和快照可能持续计费，不再需要时请通过服务控制台或 CLI 清理。删除配置只删除配置本身。

寿命字段控制云端环境寿命或自动停止间隔，以及断开连接后的工作进程空闲超时。零值使用 600 秒。Docker 容器保留不受工作进程空闲超时影响。连接断开时，不会自动重放结果未知的工具操作；下一次调用可重新连接已保存的工作进程。后台进程控制始终使用环境内的进程登记信息，不操作宿主 PID。

仅转发明确列出的环境变量，变量不存在会阻止启动。变量值通过已认证的执行连接传输。不要转发工作区命令不应读取的密钥。

此执行路径由内置引擎提供。外部引擎遇到远程环境配置时会拒绝执行。子代理通过原地隔离继承环境，不支持自动创建宿主 Git worktree；需要时可在执行环境内显式创建 Git worktree。

## 用户配置

环境配置由用户管理。项目配置不能安装或覆盖执行适配器。

```json
{
  "execution_environments": {
    "default": "isolated",
    "profiles": {
      "isolated": {
        "backend": "docker",
        "image": "wuu-execution:local",
        "workspace": "/workspace",
        "network": "none",
        "cpus": 2,
        "memory_mb": 2048,
        "persistent": true
      }
    }
  }
}
```

可选字段包括 `shared`、`user`、`host_workspace`、`mount_read_only`、`forward_env`、`worker`、`lifetime_seconds`、`python`，以及 SSH 的 `host`、`port`、`identity_file`、`known_hosts_file`。命令适配器在 `command` 中填写可执行程序及参数组成的数组。适配器是受信任的本机程序，拥有 Wuu 的宿主权限。

## 使用公共 SDK 实现后端

`@wuu/plugin-sdk` 导出 `runExecutionBackendV4` 及 `ExecutionBackendV4` 类型，
直接服务既有 worker 协议，不新增宿主服务，也不是插件的 `runJSONLRuntime` 协议。
后端无需导入 Wuu 内部代码即可实现文件操作及进程管理。通过用户配置选择已安装的程序，
例如 `{ "backend": "command", "command": ["node", "/absolute/path/backend.mjs"], "workspace": "/workspace" }`。
[独立后端验收实现](../../../scripts/external-composition-acceptance/external-worker.ts)
展示了仅使用 Node API 的文件读写与列表、进程输出、恢复记录及清理。它明确拒绝受限模式
下的 shell 执行，是受信任的测试实现，不是生产沙箱。

适配器负责分帧、认证 token 绑定、请求取消和回调路由；提供者实现 `initialize`、
带类型的 `methods` 映射和 `close`：

- **握手：**以换行分隔 JSON，请求为 `{ id, method, token, data }`，回复为
  `{ id, data }` 或 `{ id, error }`，其中 `error` 是字符串。每帧含换行最多 16 MiB。
  首次初始化固定 token 以及不可变的 `{ version: 4, root, session, identity?,
  environment?, require_existing_workspace?, remove_workspace_on_exit? }` 绑定。
  初始化成功之前不执行操作，重复初始化不得更改绑定。提供者初始化被取消或失败后必须
  建立新连接。token 保护的是私有连接，不能替代远程传输认证；跨机器应使用已认证的 SSH
  或同等安全的传输。
- **文件与工具：**`execute` 接收 `ExecutionToolRequestV4`，包含工作区工具名、已编码的
  `call.arguments`、执行者和权限模式，返回普通 `ToolResult`。路径属于已初始化的后端。
  提供者负责实现操作、落实只读/路径/OS 边界、处理初始化中的保留标志与显式环境变量，
  并拒绝不支持的策略。SDK 不创建目录，不自动把适配器环境传给命令，也不提供本地文件
  系统回退。
- **进程：**实现 `process/start`、`process/list`、`process/get`、`process/read`、
  `process/stop` 及实际支持的其他控制操作。`ExecutionProcessV4` 使用 snake_case，
  `root_thread_id` 必须等于 `binding.session`。读取选项和结果刻意保留 Go wire 大小写：
  `MaxBytes`、`OffsetBytes`、`Wait`、`MinDwell`，以及 `Process`、`Output`、`StartOffset`、
  `EndOffset`、`TotalBytes`、`Truncated`、`TimedOut`、`Duration`。这些进程字段中的时长
  单位为**纳秒**，不同于 `timeout_ms`。`emitProcess` 在同一连接发布
  `{ Type, Cause, Process }` 事件。
- **接纳与恢复：**只有启动回复及 `process/list` 返回的持久记录均保留宿主
  `operation_id` 时，才显式声明 `capabilities: ["process.start.v1"]`。辅助函数检查
  回复标识并要求两个 handler 及 `close` 都存在，持久性由提供者保证。此能力不承诺重复启动去重；
  SDK 不重放请求，回复丢失也不代表操作没有产生效果。
- **生命周期：**handler 可以并发，需保护共享状态。`context.signal` 随对应请求结束，
  `cancel` 中止它，终态回复等待 handler 实际退出。已接纳的后台进程不能将此信号作为
  生命周期；应使用 `context.lifetimeSignal`，并在 `close` 中停止所属进程树。EOF、
  格式错误或输出失败先取消并等待请求退出，再清理资源；输出失败后，阻塞的输入也不能
  拖延清理。不支持的方法明确报错，不会改用本地实现。
- **代码与产物：**可选的 `run_code` handler 只能在自身父请求有效期间调用
  `context.invokeTool`。版本 4 要求每次程序的 `result_view` 语义，不能忽略它仍声称执行
  成功。产物通过 `export/read` 按 token 和字节偏移读取，返回 base64 `{ data, eof }`，
  随后调用 `export/close`。提供者负责不可变快照；不支持的 handler 应省略，而非伪造成功。

局部扩展后端时，`delegateExecutionBackendV4` 提供显式 peer 客户端，不查找或自动启动
默认 worker：

```ts
const peer = delegateExecutionBackendV4({
  command: ["node", "/absolute/path/selected-peer.mjs"],
  capabilities: ["process.start.v1"],
});
await runExecutionBackendV4({
  ...peer,
  methods: {
    ...peer.methods,
    async execute(request, context) {
      // Add an audit operation here, then explicitly delegate unchanged input.
      return peer.methods.execute!(request, context);
    },
  },
});
```

peer 仅在初始化时启动，必须接受相同的完整绑定、协议版本、工作区根目录及要求的能力。
每次调用保留该绑定和当前取消信号。代码回调使用原始外层请求的 `invokeTool`，进程事件
仍归外层连接生命周期。peer 缺失或失败时不重试、不替换。传输终态失败会立即关闭 peer，
即使外层输入仍保持打开。取消最多等待 35 秒取得 peer 的终态回复，随后关闭无响应的 peer。
所启动的 peer 通过 EOF 开始清理，3 秒后仍未退出会被终止，并报告清理失败，不能据此声称
所有远程资源已停止。也可显式提供 `{ input, output, close }` 传输代替 `command`，此时
`close` 负责物理清理。未提供 `env` 时，子进程继承适配器环境，因此只能选择可信 peer，
必要时应显式提供受限环境。

委派必须保持同一执行世界。如果文件覆盖改用无关的内存或远程文件系统，应省略委派的
进程方法和 `process.start.v1`，而非让命令读取另一套文件。
[审计及内存文件系统验收实现](../../../scripts/external-composition-acceptance/README.md)
通过真实宿主验证这两种选择。

插件消费者使用独立的[会话绑定进程 API](../customize/plugin-authoring.md#绑定会话的-shell-进程)。
`getProcessEnvironment` 和已接纳进程的快照公开所选位置，但这些元数据不能用来改变绑定或
获取权限。

## 可重复验证

构建当前执行镜像并启动 Docker 后运行：

```sh
WUU_EXECUTION_E2E_IMAGE=wuu-execution:local go test ./internal/executionworker -run TestDocker -count=1 -v
```

Singularity/Apptainer 检查将 `WUU_EXECUTION_E2E_SIF` 指向包含当前执行程序的 SIF 镜像，运行 `go test ./internal/executionworker -run TestSingularity -count=1 -v`。

SSH 检查使用一次性测试服务器：将 JSON 格式的 SSH 配置赋给 `WUU_EXECUTION_E2E_SSH`，运行 `go test ./internal/executionworker -run TestSSHEnvironmentEndToEnd -count=1 -v`。测试会在专属环境目录内创建文件和进程。

桌面验收运行 `npm --prefix desktop run test:e2e:execution-environments`。它使用真实渲染器和模拟设置桥接，将截图及 JSON 记录保存到 `desktop/out/e2e/execution-environments`，不验证云端账号。云端实测需要对应凭据及已发布到该服务的执行镜像。

## Worker 兼容性

宿主与 worker 必须使用执行协议版本 4。升级宿主时也需要更新 worker 可执行文件
或重建镜像。旧 worker 在执行前就会被拒绝：程序要求仅通过工具调用的解释器
隔离、按执行者隔离的 JSON 状态和每次程序的结果视图选择，不能回退到原生程序权限。此次升级不扩大任何现有权限。

`host.process` 的远程启动还要求版本 4 握手中声明新增的 `process.start.v1` 能力。
较旧的版本 4 worker 仍可处理其已有方法，但不能在插件进程启动时悄悄忽略新的操作标识契约。
