# 视频输入

桌面端 Harness 与 Collaboration 的输入框支持粘贴、拖入和选择 MP4、MOV、WebM 文件。附件和消息记录可播放视频，并显示文件名、时长和大小。播放能力取决于本机支持的编码。

当前每次发送的视频附件合计上限为 20 MiB。此版本通过有大小限制的内联数据传输短视频；未实现大文件分片上传、供应商 Files API、自动转码或抽帧。视频中的音频是否被理解取决于所选模型。

## 接入范围

视频准入同时检查模型能力和连接协议。登录成功、模型名称包含视觉关键词、模型可以生成视频，都不等于当前连接可以接收视频。

| 接入方式 | 当前行为 | 依据 |
| --- | --- | --- |
| OpenRouter Chat | 模型目录声明 video 输入时，发送 `video_url` 数据 URL；实际下游仍可能有格式和时长限制 | [OpenRouter 视频输入](https://openrouter.ai/docs/guides/overview/multimodal/videos) |
| 阿里云 DashScope / Model Studio Chat | 模型目录声明 video 输入时，发送 `video_url`；按实际区域的模型权限执行 | [Qwen 图像与视频理解](https://docs.modelstudio.console.alibabacloud.com/zh/model-studio/vision) |
| 自定义 Chat 兼容接口 | 支持显式声明 `options.video_input`；未知能力时不会凭模型名称推断 | 接入方须实现相同的视频内容格式 |
| OpenAI API / Codex 登录复用 | 当前 Responses 视频输入未接入；明确拒绝新视频附件 | [Responses 请求格式](https://developers.openai.com/api/reference/cli/resources/responses/methods/create)及当前适配器 |
| Grok CLI / Grok Build 登录复用 | 未确认订阅接口支持本地视频，保持关闭 | 当前 Grok Build 适配器只确认 Chat 传输，不推断视频能力 |
| SuperGrok OAuth | 当前 Responses 适配器关闭视频输入 | [X Search 视频理解](https://docs.x.ai/developers/tools/x-search)是工具能力，不能视为本地文件上传协议 |
| Gemini 官方直连兼容接口 | 当前不开放；Gemini 原生视频接口尚未接入 | [Gemini 原生视频理解](https://ai.google.dev/gemini-api/docs/video-understanding)与[兼容接口](https://ai.google.dev/gemini-api/docs/openai)是不同契约 |
| Anthropic Messages | 当前适配器不支持视频 | [Claude Vision](https://platform.claude.com/docs/en/build-with-claude/vision) |

支持集合随模型目录和用户配置变化，不硬编码模型名称。满足本地准入条件不代表每个账号和下游路由都完成了真实推理验证。

不支持的新视频会显示切换模型和连接的提示。后续改用不支持视频的模型时，旧记录保留原始附件，仅在该次模型上下文中替换为无法读取的标记；不伪装为已经分析过的视频。

## 自定义模型

在现有 provider 的 `models` 配置中合并以下字段，替换示例模型 ID：

```json
{
  "models": {
    "your-model-id": {
      "modalities": { "input": ["text", "image", "video"], "output": ["text"] },
      "options": { "video_input": "video_url" }
    }
  }
}
```

Provider 必须使用 Chat 协议。`video_input` 是 Wuu 内部配置，不会作为额外参数发给供应商。模型目录明确声明不支持视频时，应先核实并更新 `modalities`；这个选项不会覆盖否定的模型能力，也不会打开 Codex、SuperGrok、Grok Build 或 Anthropic 的视频入口。

## 验证边界

本地格式校验、模拟 HTTP 测试和界面播放不等于供应商端视频推理验收。使用自己的账号确认模型权限、下游格式与时长限制；不能把本地接入支持描述为识别准确率或账号可用性保证。实现入口见 [视频准入](../../../internal/providers/video_input.go)。
