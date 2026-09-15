package providers

import (
	"fmt"
	"net/url"
	"strings"
)

const MaxVideoBytes = 20 * 1024 * 1024

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
// possible; a newly supplied video must never be silently dropped.
func PrepareVideoInput(req ChatRequest, transport bool) (ChatRequest, error) {
	explicit := req.ProviderOptions["video_input"] == "video_url"
	supported := transport && ((req.MediaInput.VideoKnown && req.MediaInput.Video) || (!req.MediaInput.VideoKnown && explicit))
	if !supported {
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role != "user" {
				continue
			}
			for _, file := range req.Messages[i].Files {
				if IsVideoMediaType(file.MediaType) {
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
	if _, exists := req.ProviderOptions["video_input"]; exists {
		options := make(map[string]any, len(req.ProviderOptions))
		for key, value := range req.ProviderOptions {
			if key != "video_input" {
				options[key] = value
			}
		}
		req.ProviderOptions = options
	}
	req.MediaInput.Video, req.MediaInput.VideoKnown = supported, true
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
