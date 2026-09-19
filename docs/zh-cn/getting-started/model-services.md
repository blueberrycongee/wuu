# 连接模型服务

模型服务（provider）决定 Wuu 向哪里发送请求、使用什么凭据。模型（model）则是该服务接受的模型标识。同一服务可以保存多个模型，无需重复填写连接设置。

本页设置适用于 **Wuu** 引擎。选择 Codex 或 Claude Code 外部引擎时，运行的是对应程序，使用它自身的认证和配置。在 Wuu 服务中复用订阅凭据，与运行对应的外部引擎，是两种不同的用法。

## 在桌面应用中添加服务

1. 打开**设置 → 模型服务 → 新增服务**。
2. 按服务支持的 API 选择类型，例如 **OpenAI 兼容**或 **Anthropic 兼容**。
3. 填写服务标识、账号可用的模型 ID、API 端点和凭据。服务要求 `/v1` 等前缀时，应一并填入端点。
4. 保存后回到输入框，选择本次对话使用的服务和模型。
5. 发送一个包含工具调用的小请求，例如读取项目文件，确认它不只是能回复文字。

首次设置提供了较简单的连接表单。自定义端点和更多模型选项请在设置中调整。配置里列出的模型不代表你的账号一定有访问权限。

输入框中的选择属于当前对话；第一条消息发出前，它属于当前草稿。设置中还可以保存工作区默认值，切换一次对话的模型不会自动替换这些默认值。

## 使用已有订阅

| 连接方式 | 设置方法 |
|---|---|
| Codex 订阅 | 先在 Codex CLI 登录，再在首次设置中选择复用检测到的登录。手动配置时，使用 `openai-codex` 服务并启用 `reuse_codex_credentials`。Wuu 桌面端不会自行发起 OpenAI OAuth 登录。 |
| xAI SuperGrok | 添加 **xAI SuperGrok** 服务，按提示在浏览器登录。CLI 使用 `wuu login xai`，运行时选择 `--provider xai-subscription`。 |
| Grok Build | 先运行 `grok login`，再在 Wuu 中选择检测到的服务，或传入 `--provider grok-build`。登录过期后重新在 Grok CLI 登录；Wuu 不刷新或修改这类凭据。 |

SuperGrok 订阅登录、Grok CLI 登录和 `XAI_API_KEY` 是不同的凭据来源，请选择与你的账号对应的连接。文件编辑和命令执行还要求服务及模型支持工具调用。

## 配置 CLI

首次使用时创建用户配置：

```bash
wuu init
```

文件默认位于 `~/.wuu/config.json`；设置 `WUU_HOME` 后为 `$WUU_HOME/config.json`。已有文件时直接编辑，`wuu init --force` 会覆盖它。

在 `providers` 下检查 `base_url`、`model` 和 `api_key_env`。生成的配置初始选择 `openai`；如果账号需要使用其他模型，请替换示例模型。运行前设置指定的环境变量：

```bash
export OPENAI_API_KEY="你的 API key"
cd /path/to/project
wuu exec --provider openai --permission-mode read_only "阅读这个项目，告诉我怎样运行测试"
```

`--provider` 选择已配置的服务标识，`--model` 覆盖本次运行的模型。配置优先级和项目级设置的限制见[配置说明](../reference/configuration.md)。

## 检查连接失败的原因

“凭据已配置”只表示本机有可用凭据，不代表服务商已经接受它。请检查所选服务、端点、模型 ID 和账号权限。环境变量必须对启动 Wuu 的进程可见；从程序坞启动的桌面应用不一定继承终端中设置的变量。也可以在设置中保存 API key。

如果能回复文字却不能使用工具，检查服务是否支持工具调用和流式响应。兼容某种 API 格式，不代表所有模型具备相同能力。

提示词、选入上下文的内容、附件和工具结果可能通过配置的端点离开本机。费用与数据政策由服务商决定；使用网关时，请求会经过该网关。不要把真实 API key 写入项目文件或 Git 历史。
