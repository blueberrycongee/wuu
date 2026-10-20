package openai

import "fmt"

// prepareChatGPTResponsesPayload runs after option merging, so configuration
// cannot re-enable storage, unsupported fields, or persistent HTTP state.
// https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations
func prepareChatGPTResponsesPayload(object map[string]any) error {
	for _, key := range []string{"background", "conversation", "max_output_tokens", "max_tool_calls", "metadata", "moderation", "multi_agent", "prompt", "prompt_cache_retention", "safety_identifier", "temperature", "top_logprobs", "top_p", "truncation", "user", "previous_response_id", "native_tool_search", "nativeToolSearch"} {
		delete(object, key)
	}
	object["store"] = false
	object["stream"] = true
	input, ok := object["input"].([]any)
	if !ok || len(input) == 0 {
		return fmt.Errorf("ChatGPT Responses requires full context as an input array")
	}
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid ChatGPT input item")
		}
		if item["role"] == "system" {
			item["role"] = "developer"
		}
		switch item["type"] {
		case "function_call", "custom_tool_call":
			item["namespace"] = "wuu"
		case "tool_search_call", "tool_search_output":
			return fmt.Errorf("ChatGPT plan inference does not support Responses tool_search")
		}
	}
	tools, ok := object["tools"].([]any)
	if !ok {
		if object["tools"] != nil {
			return fmt.Errorf("invalid ChatGPT tools")
		}
		return nil
	}
	if len(tools) == 0 {
		return nil
	}
	local := make([]any, 0, len(tools))
	hosted := make([]any, 0)
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid ChatGPT tool")
		}
		switch tool["type"] {
		case "function", "custom":
			delete(tool, "defer_loading")
			local = append(local, tool)
		case "web_search":
			hosted = append(hosted, tool)
		default:
			return fmt.Errorf("ChatGPT plan inference does not support tool type %q", tool["type"])
		}
	}
	if len(local) > 0 {
		hosted = append(hosted, map[string]any{"type": "namespace", "name": "wuu", "description": "Tools executed locally by Wuu.", "tools": local})
	}
	object["tools"] = hosted
	return nil
}
