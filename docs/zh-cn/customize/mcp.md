# MCP 服务

MCP 让 Wuu 引擎使用本地进程或远程服务提供的工具。连接后，告诉 agent 服务名称和任务即可。Wuu 当前发现并调用工具，不提供资源或提示浏览器，也不支持 MCP 发起的额外用户输入交换。

## 添加服务

在用户配置的 `mcp_servers` 中添加定义，默认文件为 `~/.wuu/config.json`，然后重启 Wuu。桌面可以管理已有定义，但没有服务定义编辑器。

以下片段配置一个本地服务和一个远程服务。请替换为实际可执行程序、脚本路径和 URL：

```json
{
  "mcp_servers": {
    "project-tools": {
      "command": "node",
      "args": ["/absolute/path/mcp-server.js"],
      "env": { "PROJECT_ID": "demo" }
    },
    "docs": {
      "url": "https://mcp.example.com/mcp",
      "transport": "http"
    }
  }
}
```

对于 stdio，Wuu 直接使用 `args` 启动程序，不经过 shell。子进程继承进程环境，`env` 覆盖同名变量。服务必须把 stdout 留给协议消息，日志写到 stderr。

远程服务中，`http` 和 `streamable-http` 选择流式 HTTP，`sse` 选择旧式 SSE。省略 transport 时先探测 HTTP，端点不兼容才回退 SSE；网络不可达不会触发回退。明确指定传输方式时也不会回退。

远程 `headers` 使用字面值。原生 `mcp_servers` 不展开 `${VAR}` 占位符。不要把秘密提交到配置中，也不要把占位符误当成凭据引用。`enabled: false` 保留定义，但启动时不连接。

## 管理连接

打开**设置 → 通用 → MCP 服务**，查看连接状态、工具数量和错误，并执行连接、断开或刷新。断开只结束当前连接，不删除定义；刷新使用已加载配置重新连接并发现工具。

启用开关保存启动偏好。需要立即改变连接状态时使用连接控件；修改磁盘上的定义后重启 Wuu。刷新不会重新读取配置文件。

模型可见的工具名类似 `mcp_docs_search`，不支持的字符和过长名称会规范化。大型或低频工具定义可能延迟展示，再通过工具搜索发现。当前工具集和权限策略仍可能限制工具可用性或执行。

## 项目 `.mcp.json`

Wuu 读取项目配置目录中的 Claude 风格 `.mcp.json`：

```json
{
  "mcpServers": {
    "local-docs": {
      "command": "node",
      "args": ["/absolute/path/docs-server.js"],
      "env": { "API_TOKEN": "${API_TOKEN}" }
    }
  }
}
```

条目默认不加载。检查服务后，在不提交的 `.wuu/settings.local.json` 中批准其名称：

```json
{
  "mcp_json": { "enabled": ["local-docs"] }
}
```

`enable_all: true` 启用除 `disabled` 外的全部条目，明确禁用的名称始终优先。只需要部分服务时，优先逐个填写名称。

与原生定义不同，`.mcp.json` 会在命令、参数、环境值、URL 和请求头中展开 `${VAR}` 与 `${VAR:-default}`。缺少变量且无默认值时保留原文，并发出警告。支持的类型为 `stdio`、`http` 和 `sse`。同名原生 `mcp_servers` 条目优先。

## 远程 OAuth

远程 URL 服务可以使用 OAuth 发现、PKCE、scope 和动态客户端注册。普通定义不必预先填写 `oauth`，遇到认证要求后可从设置中登录。

当前桌面流程打开授权地址后，需要你把返回的授权码粘贴回设置，不会启动自动回调监听器。令牌进入 Wuu 凭据存储，而不是写回服务定义。

服务要求固定客户端、回调或 scope 时，在该服务下加入 `oauth` 对象：

```json
{
  "redirect_uri": "http://127.0.0.1:8765/callback",
  "client_id": "your-client-id",
  "scopes": ["tools:read"]
}
```

请使用服务实际提供的注册值；服务也可能要求 `client_secret`。配置的资源请求头不会转发给跨源授权服务器。

## 工具元数据

服务定义可以通过 `tool_overrides` 修正工具的 `read_only`、`concurrency_safe` 或 `capability` 元数据。只有明确知道操作语义时才设置。把写操作误标为安全读取，可能绕过依赖这些声明的保护。

## 排查与信任

服务未出现时，检查配置拼写、重启 Wuu，并在使用 `.mcp.json` 时确认批准状态。本地连接错误应检查程序发现、依赖、环境和 stdout 内容；远程错误应检查 URL、传输方式、网络、请求头和 OAuth 要求。

已连接且有工具的服务，仍可能使用延迟展示；可以让 agent 搜索所需工具。服务描述和结果是外部内容，不是系统指令。本地 MCP 进程使用继承的环境运行受信任代码，远程服务则会收到调用参数。连接陌生服务前，请阅读[安全模型](../reference/security-model.md)。
