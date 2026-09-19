# Video input

The desktop Harness and Collaboration composers accept pasted, dropped and selected MP4, MOV and WebM files. Attachments and messages can play videos and show their name, duration and size. Playback depends on locally supported codecs.

Video attachments total at most 20 MiB per send. Short videos use bounded inline data; chunked large-file upload, provider Files APIs, automatic transcoding and frame extraction are not implemented. Understanding the audio depends on the selected model.

## Supported connections

Admission checks both model capability and connection protocol. Successful login, a vision-related model name or video generation support does not establish video input support.

| Connection | Current behavior | Reference |
| --- | --- | --- |
| OpenRouter Chat | Sends a `video_url` data URL when the model catalog declares video input; downstream format and duration limits still apply | [OpenRouter video input](https://openrouter.ai/docs/guides/overview/multimodal/videos) |
| Alibaba DashScope / Model Studio Chat | Sends `video_url` when the model catalog declares video input; regional account permissions apply | [Qwen image and video understanding (Chinese)](https://docs.modelstudio.console.alibabacloud.com/zh/model-studio/vision) |
| Custom Chat-compatible endpoint | Supports an explicit `options.video_input` declaration; unknown capabilities are not inferred from names | The endpoint must implement the same video content format |
| OpenAI API / Codex login reuse | The current Responses adapter rejects new video attachments | [Responses request format](https://developers.openai.com/api/reference/cli/resources/responses/methods/create) and the current adapter |
| Grok CLI / Grok Build login reuse | Local video support on the subscription endpoint is unconfirmed and remains disabled | The adapter's Chat transport alone does not imply video support |
| SuperGrok OAuth | The current Responses adapter disables video input | [X Search video understanding](https://docs.x.ai/developers/tools/x-search) is a tool capability, not a local upload protocol |
| Direct Gemini compatibility endpoint | Not enabled; the native Gemini video API is not integrated | [Native video understanding](https://ai.google.dev/gemini-api/docs/video-understanding) and the [compatibility endpoint](https://ai.google.dev/gemini-api/docs/openai) have different contracts |
| Anthropic Messages | The current adapter does not support video | [Claude Vision](https://platform.claude.com/docs/en/build-with-claude/vision) |

The supported set follows the model catalog and user configuration, not hardcoded model names. Local admission does not mean every account and downstream route has passed real inference verification.

Unsupported new videos prompt you to switch models and connections. Switching to an unsupported model later preserves old attachments in history but substitutes an unreadable-video marker in that request's model context; Wuu does not pretend the video was analyzed.

## Custom models

Merge these fields into an existing provider's `models` configuration, replacing the example ID:

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

The provider must use Chat. `video_input` is internal Wuu configuration, not an extra parameter sent to the provider. If the catalog explicitly denies video support, verify and update `modalities` first. This option does not override a negative capability or enable Codex, SuperGrok, Grok Build or Anthropic video input.

## Verification limits

Local validation, simulated HTTP tests and UI playback do not establish provider-side inference support. Verify model access, downstream format and duration limits with your own account; local integration is not a guarantee of recognition accuracy or account availability. See the [video admission implementation](../../../internal/providers/video_input.go).
