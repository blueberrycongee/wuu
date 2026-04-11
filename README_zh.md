# wuu

终端原生 AI 编程助手，Go 编写。

作者姓 Wu，所以叫 wuu —— 目标是把它打磨到让每个开发者写代码时都忍不住 *wuuuuu!*

## 安装

```bash
# Homebrew
brew install blueberrycongee/tap/wuu

# 脚本安装
curl -fsSL https://raw.githubusercontent.com/blueberrycongee/wuu/main/install.sh | sh

# npm
npx wuu@latest

# 从源码
go install github.com/blueberrycongee/wuu/cmd/wuu@latest
```

## 快速开始

```bash
wuu                           # 启动交互式 TUI
wuu init                      # 生成配置模板
wuu run "描述一下这个仓库"       # 单次任务
```

首次启动时，`wuu` 会引导你完成 provider 配置（API key、模型、主题）。

## 版本管理

- `VERSION` 是版本号唯一来源（SemVer，例如 `0.1.0`）。
- 本地默认构建为 `vX.Y.Z-dev`：

```bash
make install
wuu version --long
```

- 发布流程：

```bash
# 1) 修改 VERSION
# 2) 根据 VERSION 创建发布 tag
make tag-release

# 3) 推送 tag，触发 GitHub Release 工作流
git push origin v$(cat VERSION)
```

推送 `v*` tag 后，会由 GitHub Actions + GoReleaser 自动发布二进制产物。

## 功能

- 交互式 TUI，支持流式 Markdown 渲染、斜杠命令、会话记忆
- Agent 工具调用循环 —— 在你的仓库里读、写、编辑、搜索、执行命令
- 支持 OpenAI 兼容 API（OpenAI / OpenRouter / one-api 等）和 Anthropic Messages API
- 内置工具：`run_shell`、`read_file`、`write_file`、`edit_file`、`list_files`、`grep`、`glob`、`web_search`、`web_fetch`
- 编排与会话工具：`ask_user`、`spawn_agent`、`fork_agent`、`send_message_to_agent`、`stop_agent`、`list_agents`、`load_skill`
- 工具可用范围：
  - 主交互代理（TUI 会话）：可用全部工具
  - 子代理：不可用 `ask_user` 与编排工具（`spawn_agent`、`fork_agent`、`send_message_to_agent`、`stop_agent`、`list_agents`）
- Follow-up 控制：`send_message_to_agent` 可向运行中的子代理排队发送简短指令；消息会在子代理下一轮模型请求前作为 user turn 注入
- 文件工具沙箱化，限制在当前工作区内
- 会话隔离，支持恢复
- 长对话自动压缩上下文

## 配置

配置文件加载优先级：

1. `.wuu.json`（项目级）
2. `wuu.json`
3. `~/.config/wuu/config.json`（全局）

```json
{
  "default_provider": "openrouter",
  "providers": {
    "openrouter": {
      "type": "openai-compatible",
      "base_url": "https://openrouter.ai/api/v1",
      "api_key_env": "OPENROUTER_API_KEY",
      "model": "openai/gpt-4.1-mini"
    },
    "anthropic": {
      "type": "anthropic",
      "api_key_env": "ANTHROPIC_API_KEY",
      "model": "claude-sonnet-4-20250514"
    }
  }
}
```

## 许可证

MIT
