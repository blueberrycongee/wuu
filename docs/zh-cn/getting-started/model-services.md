# 连接模型服务

先选择服务商（provider），再选择它提供的模型（model）。服务配置保存连接地址和凭据，
模型名称填写服务商接受的模型 ID。同一服务可以配置多个模型。

提示词、相关文件、附件和工具结果可能发送给你选择的服务商。费用和数据政策由该服务商
决定；使用网关时，数据会发往配置的网关地址。不要把真实 API Key 写进项目文件或提交到 Git。

## 配置桌面应用

1. 打开**设置 → 模型服务 → 新增服务**。
2. 按服务商的 API 协议选择 **OpenAI 兼容**或 **Anthropic 兼容**。
3. 填写服务标识、模型名称、API 端点和 API Key，然后选择**添加服务**。
4. 回到对话，确认选中的服务和模型，发送一个小任务检查是否能正常回复和调用工具。

OpenAI 和 OpenRouter 等服务可使用 OpenAI 兼容类型；Anthropic 使用 Anthropic 兼容类型。
网关或本地服务按其协议选择，端点须包含服务要求的 API 前缀，例如 `/v1`。
已有服务可以在设置中添加或切换模型，无需为每个模型重复填写凭据。

## 使用订阅登录

- **Codex 订阅：**先在 Codex CLI 登录，再在 Wuu 首次设置中选择复用登录，或在
  `openai-codex` 服务配置中启用 `reuse_codex_credentials`。桌面端不能直接发起 OpenAI OAuth 登录。
- **xAI SuperGrok：**新增服务时选择 **xAI SuperGrok**，按提示完成账号登录。
  CLI 使用 `wuu login xai`，运行任务时选择 `--provider xai-subscription`。
  此连接使用 xAI 订阅登录，与 Grok CLI 登录和 `XAI_API_KEY` 分开。
- **Grok Build：**先运行 `grok login`。桌面端检测到可用的本机登录后会显示该服务，
  可直接选择；CLI 使用 `--provider grok-build`。登录过期后重新运行 `grok login`，
  Wuu 不会修改或刷新 Grok CLI 的凭据。

以上连接使用 Wuu 的 Agent 执行任务。文件编辑、命令执行需要模型和服务端都支持工具调用。

## 配置 CLI

首次使用先生成用户配置：

```bash
wuu init
```

配置默认写入 `~/.wuu/config.json`；设置 `WUU_HOME` 后写入 `$WUU_HOME/config.json`。
已有配置时直接编辑，`wuu init --force` 会覆盖文件。

在 `providers` 中确认所选服务的 `base_url`、`model` 和 `api_key_env`。
初始默认服务为 `openai`；请确认示例模型是账号可用的模型，再按 `api_key_env` 设置环境变量：

```bash
export OPENAI_API_KEY="你的 API Key"
cd /path/to/your/project
wuu exec --provider openai --permission-mode read_only "阅读这个项目，告诉我怎样运行测试"
```

`--provider` 选择配置中的服务标识，`--model` 可覆盖本次使用的模型。
正常启动时，项目配置不能替换用户的服务地址、凭据和权限模式；详细规则见
[配置说明](../reference/configuration.md)。

## 排查连接问题

“凭据已配置”只表示 Wuu 能读到凭据，不保证服务商接受它。提示缺少 API Key 时，
检查所选服务以及 `api_key_env` 对应的变量是否有值。桌面应用还需要从能读取该变量的
进程启动；也可以直接在设置中保存 API Key。

提示模型不存在时，核对模型 ID 和账号访问权限。能聊天却不能使用工具时，检查模型和
网关是否支持工具调用及流式响应。

模型服务连接完成后，继续[完成第一个任务](first-task.md)。
