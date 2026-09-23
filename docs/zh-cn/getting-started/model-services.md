# 连接模型服务

模型服务（provider）决定 Wuu 向哪里发送请求、使用什么凭据。模型（model）则是该服务接受的模型标识。同一服务可以保存多个模型，无需重复填写连接设置。

本页设置适用于 **Wuu** 引擎。选择 Codex、Claude Code、Devin 等[外部引擎](external-engines.md)时，运行的是对应程序，使用它自身的认证和配置。在 Wuu 服务中复用订阅凭据，与运行对应的外部引擎，是两种不同的用法。

## 在桌面应用中添加服务

1. 打开**设置 → 模型服务 → 新增服务**。
2. 按服务支持的 API 选择类型，例如 **OpenAI 兼容**或 **Anthropic 兼容**。
3. 填写服务标识、账号可用的模型 ID、API 端点和凭据。服务要求 `/v1` 等前缀时，应一并填入端点。
4. 保存后回到输入框，选择本次对话使用的服务和模型。
5. 发送一个包含工具调用的小请求，例如读取项目文件，确认它不只是能回复文字。

首次设置提供了较简单的连接表单。自定义端点和更多模型选项请在设置中调整。配置里列出的模型不代表你的账号一定有访问权限。

输入框中的选择属于当前对话；第一条消息发出前，它属于当前草稿。设置中还可以保存工作区默认值，切换一次对话的模型不会自动替换这些默认值。

## 当前 OpenAI 和 Anthropic 模型

目录已包含 `gpt-6-sol`、`gpt-6-luna`、`claude-opus-5-5` 和 `claude-fable-5-1`。已有会话和工作区选择保持不变，需要使用时主动切换模型。

GPT-6 Sol 和 Luna 支持 `none` 至 `max` 推理档位，默认 `medium`。Fast 条目使用同一个模型，通过优先处理提供不同速度和价格。对这两个模型，官方 OpenAI 连接未指定协议时，Wuu 默认使用 Responses。如果明确选择了 Chat Completions，使用推理和工具时请切换为 Responses；Chat Completions 仅在 `none` 档位支持它们的工具调用。自定义端点保留原有协议。详见官方 [Sol](https://developers.openai.com/api/docs/models/gpt-6-sol) 和 [Luna](https://developers.openai.com/api/docs/models/gpt-6-luna) 规格。

Claude Opus 5.5 和 Fable 5.1 始终使用自适应思考。Wuu 将已保存的 `none` 选择映射为 `low`，两者默认档位分别为 `medium` 和 `high`。Wuu 请求可读的思考摘要，并允许 API 丢弃因上下文变更而失效的思考块，同时保留有效的签名块。这两个模型不接受强制工具选择，因此 Wuu 通过指令表达收尾工具调用要求，使用自动工具选择；这不保证模型一定调用工具。详见官方 [Opus 5.5](https://platform.claude.com/docs/en/models/opus-5-5/migration-guide) 和 [Fable 5.1](https://platform.claude.com/docs/en/models/fable-5-1/migration-guide) 迁移指南。

## 使用已有订阅

| 连接方式 | 设置方法 |
|---|---|
| Codex 订阅 | 先在 Codex CLI 登录，再在首次设置中选择复用检测到的登录。手动配置时，使用 `openai-codex` 服务并启用 `reuse_codex_credentials`。Wuu 桌面端不会自行发起 OpenAI OAuth 登录。 |
| xAI SuperGrok | 添加 **xAI SuperGrok** 服务，按提示在浏览器登录。CLI 使用 `wuu login xai`，运行时选择 `--provider xai-subscription`。 |
| Grok Build | 先运行 `grok login`，再在 Wuu 中选择检测到的服务，或传入 `--provider grok-build`。登录过期后重新在 Grok CLI 登录；Wuu 不刷新或修改这类凭据。 |

SuperGrok 订阅登录、Grok CLI 登录和 `XAI_API_KEY` 是不同的凭据来源，请选择与你的账号对应的连接。文件编辑和命令执行还要求服务及模型支持工具调用。

## 在桌面端查看订阅

**设置 → 订阅**集中显示已安装的外部 Agent 和内置订阅服务。各来源保留各自的模型和认证方式；展开**详情**可查看请求信息或登录 ACP。

请求状态、错误和已上报用量按当次请求记录归属，对话之后切换供应商不会改变历史归属。较早请求的用量不会显示为之后失败请求的用量。旧记录无法证明来源时保持未知；CLI 的静态模型列表本身也不能证明登录状态。

Codex 账户额度通过已安装的 CLI 读取，显示剩余百分比和重置时间。额度重置或快照超过五分钟后需要刷新。其他来源在接入账户额度查询前显示**未提供**；ACP 的上下文窗口占用不是订阅额度。**Wuu 内用量**汇总保留历史中已上报的 token，输入含缓存；未上报用量和 Wuu 外的使用不计入，也不代表账单。

## 配置 CLI

首次使用时创建用户配置：

```bash
wuu init
```

文件默认位于 `~/.wuu/config.json`；设置 `WUU_HOME` 后为 `$WUU_HOME/config.json`。已有文件时直接编辑，`wuu init --force` 会覆盖它。

在 `providers` 下检查 `base_url`、`model` 和 `api_key_env`。生成的配置初始选择 `openai`，通过 Responses 使用 GPT-6 Sol；Anthropic 条目使用 Claude Opus 5.5。如果账号需要使用其他模型，请替换示例模型。运行前设置指定的环境变量：

```bash
export OPENAI_API_KEY="你的 API key"
cd /path/to/project
wuu exec --provider openai --permission-mode read_only "阅读这个项目，告诉我怎样运行测试"
```

`--provider` 选择已配置的服务标识，`--model` 覆盖本次运行的模型。配置优先级和项目级设置的限制见[配置说明](../reference/configuration.md)。

## 回合结束与额外请求

Wuu 按所选 API 的结束信号处理回合。正常停止会结束回合，即使响应没有正文；这不代表任务一定完成。仅有空文本或过程说明，不会触发另一次收费请求。

Anthropic Messages 可通过 `pause_turn` 明确要求继续。Responses 兼容服务可在成功完成的响应中使用可选扩展 `end_turn: false` 请求继续；缺失、null 或 true 都不表示需要继续。标准 OpenAI Responses API 不要求提供此扩展。Chat Completions 使用自身的 `finish_reason`；Wuu 不根据网关原生原因或其他 API 的字段猜测是否继续。输出上限、内容过滤、错误和未知停止原因本身都不会触发续跑。

每次续跑都是新的模型请求，可能产生费用。Wuu 最多连续自动续跑八次而不执行客户端工具；如果服务仍要求继续，则报告错误。执行客户端工具后重新计数，配置的步数上限和取消操作仍然有效。尚未结束的响应不能作为压缩摘要替换对话历史。

## 让 Agent 读取本地图片

直接告诉 Agent 图片路径，例如：“读取 `screenshots/settings.png`，检查对齐。”
使用 Wuu 内置工具时，`read_file` 会把 PNG、JPEG、静态 GIF 和 WebP 作为视觉输入
交给支持图片的模型，无需先从输入框附加图片。相对路径以会话工作区为基准；绝对路径
遵守与普通读取相同的文件范围和敏感路径规则。会话产物目录中的生成图片也可以读取。

文件内容决定格式。大图复用附件处理逻辑，将最长边缩小到 2048 像素，结果会说明原始
尺寸和实际传给模型的尺寸。单次读取最多接受 20 MiB 源文件和 4000 万源像素，处理后的
图片还需满足工具结果的 2 MiB 内联限制。超限时请先裁剪或缩小。损坏、动画和不支持的
图片格式会报错；SVG 仍作为源码文字读取。行范围和续读参数只适用于文本。

明确标记为仅支持文字的模型会收到图片不受支持的提示，而非图片像素；请选择支持图片
的模型进行查看。切换模型不会删除历史中保存的图片结果。启用可选的 Code Mode 时，
用 `image(part)` 转发图片内容块；`text(result)` 只输出文字。例如：

```javascript
const result = await tools.read_file({path: "screenshots/settings.png"});
for (const part of result.content) {
  if (part.type === "image") image(part);
  else if (part.type === "text") text(part.text);
}
```

`present_artifact` 用于向用户展示交付物，不会替模型查看图片。外部 Agent 引擎使用
各自的文件与读图工具。

## 大型工具结果

Wuu 保留原始工具结果，并向模型提供稳定、有限的视图。普通大文本先显示连续的第一页，并附带 `read_file` 续读入口；续读读取已保存的结果，不会重新执行原工具。分页优先保留完整行，超长单行可以分段读取而不拆坏 Unicode 字符。内容发生变化时，续读会拒绝请求，避免混用不同版本。图片等受支持的媒体继续通过服务对应的独立表示传递。

内置工具的视图保留有用的结构：搜索分页保留完整记录和快照游标，命令输出优先展示最近的错误证据。工具账本记录结果前就会固定视图，扩展结果和执行错误也走这条路径，因此后续请求和重放看到的视图保持一致。Wuu 不再为了整批文本限额二次切断这些页面；对话容量仍由上下文管理处理。分页可能增加模型请求次数；无法安全保存或分页时，Wuu 保留完整结果。页面更小并不保证总费用更低。

## 检查连接失败的原因

“凭据已配置”只表示本机有可用凭据，不代表服务商已经接受它。请检查所选服务、端点、模型 ID 和账号权限。环境变量必须对启动 Wuu 的进程可见；从程序坞启动的桌面应用不一定继承终端中设置的变量。也可以在设置中保存 API key。

如果能回复文字却不能使用工具，检查服务是否支持工具调用和流式响应。兼容某种 API 格式，不代表所有模型具备相同能力。

提示词、选入上下文的内容、附件和工具结果可能通过配置的端点离开本机。费用与数据政策由服务商决定；使用网关时，请求会经过该网关。不要把真实 API key 写入项目文件或 Git 历史。
