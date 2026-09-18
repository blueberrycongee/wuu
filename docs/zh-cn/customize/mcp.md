# MCP 服务器

MCP（Model Context Protocol）让 Wuu 连接本地或远程工具服务器。连接成功后，服务器提供
的工具会加入 Agent 的工具集合；工具较多时，Wuu 会延后展示低频工具，Agent 仍可按需
搜索和调用。

当前接入面只发现和调用 MCP tools，不提供面向用户的 MCP resources 或 prompts 浏览
入口。

MCP 工具会增加上下文和外部访问范围。只连接可信服务器，并只启用当前工作真正需要的
工具。

## 配置服务器

桌面设置可以管理已有服务器，但当前不能新建或编辑服务器定义。先在用户配置
`~/.wuu/config.json` 的 `mcp_servers` 中添加定义，再重新启动 Wuu。

### 本地 stdio 服务器

```json
{
  "mcp_servers": {
    "project-tools": {
      "command": "npx",
      "args": ["-y", "@example/project-mcp"],
      "env": {
        "PROJECT_ID": "demo"
      }
    }
  }
}
```

Wuu 会把 `command` 作为子进程启动，并通过 stdin/stdout 使用 MCP。不要把不可信仓库
提供的命令直接加入用户配置。`args` 会直接传给程序，不经过 shell；`env` 只用于 stdio
子进程，并覆盖 Wuu 进程中的同名变量。

### 远程服务器

```json
{
  "mcp_servers": {
    "docs": {
      "url": "https://mcp.example.com/mcp",
      "transport": "http",
      "headers": {
        "Authorization": "Bearer YOUR_TOKEN"
      }
    }
  }
}
```

`transport` 可设为 `http`、`streamable-http` 或兼容旧服务器的 `sse`。省略时会先尝试
streamable HTTP；若协议探测和旧版初始化 POST 表明该端点不支持它，再回退到 SSE。

Wuu 会自动协商支持的协议。当前不支持需要额外用户输入交互的工具结果，遇到时会报错。

`headers` 只用于远程请求。原生 `mcp_servers` 不展开 `${VAR}`，示例中的 token 等值会
按字面量使用；不要把密钥写进团队共享的项目配置。

每个服务器还可以设置 `enabled`。`false` 会保留定义但不连接；桌面端的开关会把这项
选择写回配置。

## 在桌面端管理

打开**设置 → 常规 → MCP 服务器**。每一行会显示连接状态和已发现的工具数，并提供：

- 开启或关闭服务器；
- 连接或断开；
- 刷新连接和工具列表；
- 对需要 OAuth 的远程服务器登录或移除登录；
- 查看连接错误。

启停开关会保存 `enabled` 配置，但当前连接不保证随开关立即建立或结束；需要马上改变
连接时使用旁边的连接/断开按钮，或重启 Wuu。“断开”只结束当前连接，不会删除定义。
修改磁盘配置后，重启 Wuu 最稳妥；刷新只会使用已载入的服务器配置重新连接和发现工具，
不会重新读取配置文件。

## 使用工具

连接后，直接告诉 Agent 使用该服务，例如：

```text
使用 docs MCP 查找这个 API 的最新说明，然后给出带来源的结论。
```

模型可见名称采用 `mcp_<服务器名>_<工具名>`，不兼容字符会被替换，过长名称会截断并
附加哈希。普通任务通常不需要记住完整名称，说明服务器名和目标即可。

MCP 工具仍受当前工具表面、权限模式和本地策略限制。服务器提供的描述和返回元数据都
按不可信外部内容处理，不会变成 Wuu 的系统指令。

## 使用项目 `.mcp.json`

Wuu 兼容仓库根目录的 Claude Code 风格 `.mcp.json`：

```json
{
  "mcpServers": {
    "local-docs": {
      "command": "node",
      "args": ["scripts/mcp-server.js"],
      "env": {
        "API_TOKEN": "${API_TOKEN}"
      }
    }
  }
}
```

项目文件中的服务器**默认不会加载**，因为 stdio 定义可以执行仓库指定的程序。建议在
不会提交的 `.wuu/settings.local.json` 中逐个批准：

```json
{
  "mcp_json": {
    "enabled": ["local-docs"]
  }
}
```

也可以使用 `enable_all: true` 批准全部，再用 `disabled` 拒绝个别名称。`disabled` 始终
优先：

```json
{
  "mcp_json": {
    "enable_all": true,
    "disabled": ["unsafe-server"]
  }
}
```

`.mcp.json` 支持 `stdio`、`http` 和 `sse`，并在 command、args、env、URL 和 headers
中展开 `${VAR}` 与 `${VAR:-default}`。缺失的环境变量会产生 stderr 警告。若它与原生
`mcp_servers` 使用同名定义，原生配置优先。

## OAuth

OAuth 只适用于远程 URL 服务器。Wuu 会根据服务端的 `WWW-Authenticate` 响应和标准
well-known 元数据发现受保护资源、授权服务器、PKCE、scope 及动态客户端注册信息。
普通远程定义不必预先加入 `oauth`；服务器返回 401 后，可直接在设置页开始登录。

当前桌面流程仍需用户手工回填授权码，没有对应的 CLI 登录命令或自动回调监听。默认
回调地址为 `http://127.0.0.1/callback`。服务方要求固定客户端、特殊回调地址或指定
scope 时，可以加入 `oauth` 覆盖发现结果：

```json
{
  "mcp_servers": {
    "issues": {
      "url": "https://mcp.example.com/mcp",
      "transport": "http",
      "oauth": {
        "redirect_uri": "http://127.0.0.1:8765/callback",
        "client_id": "YOUR_CLIENT_ID",
        "client_secret": "YOUR_CLIENT_SECRET",
        "scopes": ["tools:read", "tools:execute"]
      }
    }
  }
}
```

如果省略 `client_id`，Wuu 会尝试使用服务器公布的动态客户端注册端点。桌面端选择登录
后会打开授权地址；授权完成后，把回调中的 code 粘贴回设置页完成登录。令牌由 Wuu 的
凭据存储保存，不写回服务器配置。配置中的自定义 headers 只会发送给 MCP 资源端及同源
发现端点，不会转发给跨域授权服务器。

## 工具元数据覆盖

只有在你信任并了解服务器工具语义时，才用 `tool_overrides` 修正服务器声明：

```json
{
  "mcp_servers": {
    "docs": {
      "url": "https://mcp.example.com/mcp",
      "tool_overrides": {
        "search": {
          "read_only": true,
          "concurrency_safe": true,
          "capability": "search.semantic"
        }
      }
    }
  }
}
```

错误地把写操作标成只读或并发安全，可能绕过应有的串行和权限保护。不要仅依据工具名
猜测这些值。

## 排错

### 设置中没有服务器

确认定义位于 `mcp_servers`，JSON 字段没有拼错，并重新启动 Wuu。项目 `.mcp.json`
还必须通过 `mcp_json` 审批；未审批项只会写入 stderr 提示，不会出现在已配置列表中。

### 本地服务器连接失败

在相同环境中确认 `command` 可执行，依赖已安装，并确保服务器把协议消息写到 stdout、
把普通日志写到 stderr。

### 远程服务器失败

检查 URL、transport、headers 和网络代理。旧 SSE 端点应显式使用 `sse`；现代端点
优先使用 `http`。

### 需要认证或注册

确认服务器在 401 响应或标准 well-known 地址公布了 OAuth discovery 元数据。动态注册
失败时，在 `oauth` 中改用服务方提供的 `client_id` 和 `client_secret`；服务方要求特定
回调地址时，同时配置 `redirect_uri`。

### 工具没有出现在模型面前

先确认状态为“已连接”且工具数大于零。大量或超大工具可能被 Wuu 延后展示，Agent 可用
工具搜索按需发现；与当前会话工具表面不兼容的工具则不会开放。
