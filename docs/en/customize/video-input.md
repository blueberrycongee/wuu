# Video input

Attach an MP4, MOV, or WebM file by pasting, dropping, or selecting it in the desktop conversation composer. The attachment can show its name, duration, and size and play locally when the installed codecs support it.

The combined video size is limited to **20 MiB per send**. Wuu sends bounded inline data; it does not transcode the clip, extract frames, upload chunks, or use a provider Files API for large videos. Whether the model understands the audio depends on that model.

## Choose a compatible model and connection

Video input needs both model support and a compatible request format. A successful login, an image-capable model, or a video-generation feature is not enough.

| Connection | Wuu's current admission rule |
|---|---|
| OpenRouter Chat | Accepts a model catalog entry declaring video input and sends `video_url` |
| DashScope / Model Studio Chat | Uses the same video format when the model declares support |
| Custom Chat-compatible service | Can explicitly declare `options.video_input: "video_url"` |
| OpenAI Responses, including Codex login reuse | Rejects new video input |
| SuperGrok OAuth, Grok Build, Anthropic Messages | Video input is disabled in the adapters |
| Direct Gemini compatibility endpoint | Not enabled automatically; Wuu does not integrate the native Gemini video API |

An unsupported new attachment produces an error asking for a compatible model and connection. Switching to an unsupported model later keeps old attachments in history but represents them as unreadable video in that request's context.

## Declare support for a custom model

Add these fields to the model entry in an existing provider configuration, using the actual model ID:

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

The endpoint must actually accept inline `video_url` content through Chat. `video_input` is a Wuu admission option and is removed from provider request options. It cannot override a model that explicitly denies video capability or enable video in an adapter that disables it.

## If a clip fails

Check the total size, file format, selected model, and connection protocol first. Local playback and admission checks do not prove that a provider can analyze the clip. Your account and downstream route can impose additional duration, format, and access limits. Test with a short, non-sensitive clip before relying on the result.
