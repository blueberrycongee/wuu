# wuu

一个用 Go 实现的开源 CLI Coding Agent（MVP），目标是做成类似 Claude Code CLI / Codex CLI 的可扩展版本。

当前版本优先解决两件事：

1. 本地可运行的 tool-calling 编码循环。
2. 对 OpenAI-compatible 模型与中转服务的统一接入。

## 当前能力（MVP）

- `wuu init`：生成 `.wuu.json` 配置模板。
- `wuu run "任务"`：执行一次 coding 任务。
- `wuu tui`：交互式终端会话（固定底部输入区与时钟、流式输出、斜杠命令）。
- 支持 OpenAI-compatible API（OpenAI / OpenRouter / one-api / New API 等常见中转）。
- 支持 Anthropic 官方 Messages API。
- 内置本地工具：
  - `run_shell`
  - `read_file`
  - `write_file`
  - `list_files`
- 文件工具默认限制在当前 workspace 内，防止路径逃逸。

## 快速开始

```bash
go build ./cmd/wuu
./wuu init
export OPENAI_API_KEY="your-key"
./wuu run "为当前项目写一个 hello world Go 程序并解释改动"
```

也可以通过 stdin 传入任务：

```bash
echo "检查当前目录 Go 代码并给出重构建议" | ./wuu run
```

## 配置文件

优先级：

1. `.wuu.json`
2. `wuu.json`
3. `~/.config/wuu/config.json`

示例：

```json
{
  "default_provider": "openrouter",
  "providers": {
    "openai": {
      "type": "openai-compatible",
      "base_url": "https://api.openai.com/v1",
      "api_key_env": "OPENAI_API_KEY",
      "model": "gpt-4.1"
    },
    "openrouter": {
      "type": "openai-compatible",
      "base_url": "https://openrouter.ai/api/v1",
      "api_key_env": "OPENROUTER_API_KEY",
      "model": "openai/gpt-4.1-mini",
      "headers": {
        "HTTP-Referer": "https://github.com/your/repo",
        "X-Title": "wuu"
      }
    },
    "oneapi": {
      "type": "openai-compatible",
      "base_url": "https://your-one-api.example.com/v1",
      "api_key_env": "ONEAPI_API_KEY",
      "model": "gpt-4o-mini"
    }
  },
  "agent": {
    "max_steps": 8,
    "temperature": 0.2,
    "system_prompt": "You are a pragmatic CLI coding agent. Use tools when needed."
  }
}
```

## 命令说明

```bash
wuu init [--force]

wuu run [flags] "task"
  --provider
  --model
  --max-steps
  --temperature
  --system-prompt
  --workdir
  --no-tools
  --timeout

wuu tui [flags]
  --provider
  --model
  --max-steps
  --temperature
  --system-prompt
  --workdir
  --no-tools
  --memory-file
  --pre-hook
  --post-hook
  --request-timeout
```

## 设计说明

核心分层：

- `internal/config`：配置加载与校验。
- `internal/providers/openai`：OpenAI-compatible 协议适配。
- `internal/providers/anthropic`：Anthropic Messages 协议适配。
- `internal/providerfactory`：provider 装配与密钥解析（OpenAI-compatible / Anthropic / Codex alias）。
- `internal/tools`：本地工具执行。
- `internal/agent`：多轮 tool-calling agent loop。
- `internal/tui`：交互式终端 UI（流式渲染 / markdown / memory / slash commands）。
- `cmd/wuu`：CLI 入口。

## 下一步（建议）

- 增加 provider 级别真实 SSE streaming（当前 TUI 为本地流式渲染）。
- 增加更细粒度文件编辑工具（patch/diff）。
- 增强 session 管理（fork/resume/worktree 语义完善）。
