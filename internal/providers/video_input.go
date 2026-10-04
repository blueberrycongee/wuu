package providers

import (
	"fmt"
	"net/url"
	"strings"
)

func IsVideoMediaType(mediaType string) bool {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "video/mp4", "video/webm", "video/quicktime", "video/mov":
		return true
	default:
		return false
	}
}

// VideoURLTransport admits documented Chat endpoints. Private compatible
// endpoints can explicitly declare their wire contract in model options.
func VideoURLTransport(baseURL string, options map[string]any) bool {
	if format, ok := options["video_input"].(string); ok {
		return format == "video_url"
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(endpoint.Hostname())
	return host == "openrouter.ai" || host == "dashscope.aliyuncs.com" ||
		host == "dashscope-intl.aliyuncs.com" || host == "dashscope-us.aliyuncs.com" || strings.HasSuffix(host, ".maas.aliyuncs.com")
}

// PrepareVideoInput checks the selected model as well as the actual transport.
// Old videos are omitted on incompatible follow-ups so switching models remains
// possible. New unsupported videos need a tool-accessible working copy, unless
// the caller requires native evidence; a path cannot satisfy that contract.
// Keep video_input available for downstream admission checks; provider
// serializers filter this local routing option from the wire payload.
func PrepareVideoInput(req ChatRequest, transport bool) (ChatRequest, error) {
	policy := ResolveVideoInputPolicy(req.MediaInput, req.ProviderOptions, transport)
	supported := policy.Video
	if !supported {
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role != "user" {
				continue
			}
			for _, file := range req.Messages[i].Files {
				if IsVideoMediaType(file.MediaType) && (file.LocalPath == "" || file.Required) {
					return req, fmt.Errorf("video input is not supported by the selected model and connection (%s / %s); choose a video-capable model with a supported video connection", req.Provider, req.Model)
				}
			}
			// Request-only runtime/plugin context also uses the user role. It
			// must not hide the latest user attachment from admission checks.
			if !req.Messages[i].Hidden {
				break
			}
		}
	}
	req.MediaInput = policy
	return req, nil
}

// OpenRouter documents MOV data URLs as video/mov rather than the stored IANA
// media type. Translate only the outgoing copy; local playback keeps QuickTime.
func VideoMessagesForEndpoint(messages []ChatMessage, baseURL string) []ChatMessage {
	endpoint, err := url.Parse(baseURL)
	if err != nil || !strings.EqualFold(endpoint.Hostname(), "openrouter.ai") {
		return messages
	}
	out := CloneChatMessages(messages)
	for i := range out {
		for j := range out[i].Files {
			if out[i].Files[j].MediaType == "video/quicktime" {
				out[i].Files[j].MediaType = "video/mov"
			}
		}
	}
	return out
}
