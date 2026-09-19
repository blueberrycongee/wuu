package modelvariant

import "strings"

func nilIfEmpty(options map[string]any) map[string]any {
	if len(options) == 0 {
		return nil
	}
	return options
}

func setOptionDefault(options map[string]any, key string, value any) {
	if _, ok := options[key]; ok {
		return
	}
	options[key] = value
}

func compatGoogleThinkingLevelEfforts(apiID string) []string {
	id := strings.ToLower(apiID)
	if !strings.Contains(id, "gemini-3") {
		return []string{"low", "high"}
	}
	if strings.Contains(id, "flash-image") {
		return []string{"minimal", "high"}
	}
	if strings.Contains(id, "pro-image") {
		return []string{"high"}
	}
	if strings.Contains(id, "flash") {
		return []string{"minimal", "low", "medium", "high"}
	}
	return []string{"low", "medium", "high"}
}

func compatGoogleThinkingBudgetMax(apiID string) int {
	id := strings.ToLower(apiID)
	if strings.Contains(id, "2.5") && strings.Contains(id, "pro") && !strings.Contains(id, "flash") {
		return 32768
	}
	return 24576
}

func compatVariantsFromEfforts(efforts []string, build func(string) map[string]any) map[string]map[string]any {
	if len(efforts) == 0 {
		return nil
	}
	out := make(map[string]map[string]any, len(efforts))
	for _, effort := range efforts {
		effort = strings.TrimSpace(effort)
		if effort == "" {
			continue
		}
		out[effort] = build(effort)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func compatReasoningEffortVariants(efforts []string) map[string]map[string]any {
	return compatVariantsFromEfforts(efforts, func(effort string) map[string]any {
		return map[string]any{"reasoningEffort": effort}
	})
}

// compatDeepSeekV4Variants returns the vendor-documented reasoning tiers for
// DeepSeek V4 and V4.1 Flash (including the canonical deepseek-flash ID).
// medium and xhigh are accepted by the API for compatibility but are mapped to
// high by DeepSeek, so wuu does not expose them as distinct tiers.
func compatDeepSeekV4Variants(wireAPI string, efforts []string) map[string]map[string]any {
	efforts = deepSeekV4Efforts(efforts)
	if strings.EqualFold(strings.TrimSpace(wireAPI), "responses") {
		return compatVariantsFromEfforts(efforts, func(effort string) map[string]any {
			if effort == "none" {
				return map[string]any{"thinking": map[string]any{"type": "disabled"}, "reasoningEffort": "none"}
			}
			return map[string]any{"thinking": map[string]any{"type": "enabled"}, "reasoningEffort": effort}
		})
	}
	return compatVariantsFromEfforts(efforts, func(effort string) map[string]any {
		if effort == "none" {
			return map[string]any{"thinking": map[string]any{"type": "disabled"}}
		}
		return map[string]any{"thinking": map[string]any{"type": "enabled"}, "reasoningEffort": effort}
	})
}

func compatDeepSeekV4AnthropicVariants(efforts []string) map[string]map[string]any {
	return compatVariantsFromEfforts(deepSeekV4Efforts(efforts), func(effort string) map[string]any {
		if effort == "none" {
			return map[string]any{"thinking": map[string]any{"type": "disabled"}}
		}
		return map[string]any{"thinking": map[string]any{"type": "enabled"}, "effort": effort}
	})
}

func deepSeekV4Efforts(efforts []string) []string {
	if len(efforts) == 0 {
		return []string{"none", "low", "high", "max"}
	}
	out := make([]string, 0, len(efforts)+1)
	seen := map[string]bool{}
	for _, effort := range efforts {
		effort = strings.ToLower(strings.TrimSpace(effort))
		if effort == "" || seen[effort] {
			continue
		}
		seen[effort] = true
		out = append(out, effort)
	}
	if !seen["none"] {
		out = append([]string{"none"}, out...)
	}
	return out
}

// compatGLM52Variants returns the vendor-documented reasoning tiers for
// GLM-5.2. GLM-5.3 has a different, always-on reasoning contract below.
func compatGLM52Variants() map[string]map[string]any {
	return map[string]map[string]any{
		"none": {"thinking": map[string]any{"type": "disabled"}},
		"high": {"reasoningEffort": "high"},
		"max":  {"reasoningEffort": "max"},
	}
}

func compatGLM53Variants() map[string]map[string]any {
	return compatVariantsFromEfforts([]string{"low", "high", "max"}, func(effort string) map[string]any {
		return map[string]any{
			"thinking":        map[string]any{"type": "enabled", "clear_thinking": false},
			"reasoningEffort": effort,
		}
	})
}

func compatGLM53AnthropicVariants() map[string]map[string]any {
	return compatVariantsFromEfforts([]string{"low", "high", "max"}, func(effort string) map[string]any {
		return map[string]any{
			"thinking": map[string]any{"type": "enabled"},
			"effort":   effort,
		}
	})
}

func compatOpenAIProviderVariantOptions(effort string) map[string]any {
	return map[string]any{
		"reasoningEffort":  effort,
		"reasoningSummary": "auto",
		"include":          []any{"reasoning.encrypted_content"},
	}
}

func compatAnthropicVariants(desc compatModelDescriptor, adaptiveEfforts []string, githubCopilotFilter bool) map[string]map[string]any {
	if len(adaptiveEfforts) > 0 {
		efforts := append([]string{}, adaptiveEfforts...)
		if githubCopilotFilter && desc.ProviderID == "github-copilot" {
			if strings.Contains(desc.APIID, "opus-4.7") {
				efforts = []string{"medium"}
			}
			filtered := make([]string, 0, len(efforts))
			for _, effort := range efforts {
				if effort != "max" && effort != "xhigh" {
					filtered = append(filtered, effort)
				}
			}
			efforts = filtered
		}
		return compatVariantsFromEfforts(efforts, func(effort string) map[string]any {
			thinking := map[string]any{"type": "adaptive"}
			if compatAnthropicOpus47OrLater(desc.APIID) {
				thinking["display"] = "summarized"
			}
			return map[string]any{
				"thinking": thinking,
				"effort":   effort,
			}
		})
	}
	if strings.Contains(desc.APIID, "opus-4-5") || strings.Contains(desc.APIID, "opus-4.5") {
		return compatVariantsFromEfforts(compatWidelySupportedEfforts(), func(effort string) map[string]any {
			return map[string]any{"effort": effort}
		})
	}
	return map[string]map[string]any{
		"high": {"thinking": map[string]any{"type": "enabled", "budgetTokens": compatAnthropicHighBudget(desc.OutputLimit)}},
		"max":  {"thinking": map[string]any{"type": "enabled", "budgetTokens": compatAnthropicMaxBudget(desc.OutputLimit)}},
	}
}

func compatAnthropicHighBudget(outputLimit int) int {
	if outputLimit <= 0 {
		return 16000
	}
	return minInt(16000, outputLimit/2-1)
}

func compatAnthropicMaxBudget(outputLimit int) int {
	if outputLimit <= 0 {
		return 31999
	}
	return minInt(31999, outputLimit-1)
}

func compatBedrockVariants(apiID string, adaptiveEfforts []string) map[string]map[string]any {
	if len(adaptiveEfforts) > 0 {
		return compatVariantsFromEfforts(adaptiveEfforts, func(effort string) map[string]any {
			reasoning := map[string]any{
				"type":               "adaptive",
				"maxReasoningEffort": effort,
			}
			if compatAnthropicOpus47OrLater(apiID) {
				reasoning["display"] = "summarized"
			}
			return map[string]any{"reasoningConfig": reasoning}
		})
	}
	if strings.Contains(apiID, "anthropic") {
		return map[string]map[string]any{
			"high": {"reasoningConfig": map[string]any{"type": "enabled", "budgetTokens": 16000}},
			"max":  {"reasoningConfig": map[string]any{"type": "enabled", "budgetTokens": 31999}},
		}
	}
	return compatVariantsFromEfforts(compatWidelySupportedEfforts(), func(effort string) map[string]any {
		return map[string]any{"reasoningConfig": map[string]any{"type": "enabled", "maxReasoningEffort": effort}}
	})
}

func compatGatewayGoogleVariants(id string) map[string]map[string]any {
	if strings.Contains(id, "2.5") {
		return map[string]map[string]any{
			"high": {"thinkingConfig": map[string]any{"includeThoughts": true, "thinkingBudget": 16000}},
			"max":  {"thinkingConfig": map[string]any{"includeThoughts": true, "thinkingBudget": compatGoogleThinkingBudgetMax(id)}},
		}
	}
	return compatVariantsFromEfforts([]string{"low", "high"}, func(effort string) map[string]any {
		return map[string]any{"includeThoughts": true, "thinkingLevel": effort}
	})
}

func compatGoogleVariants(id string) map[string]map[string]any {
	if strings.Contains(id, "2.5") {
		return map[string]map[string]any{
			"high": {"thinkingConfig": map[string]any{"includeThoughts": true, "thinkingBudget": 16000}},
			"max":  {"thinkingConfig": map[string]any{"includeThoughts": true, "thinkingBudget": compatGoogleThinkingBudgetMax(id)}},
		}
	}
	return compatVariantsFromEfforts(compatGoogleThinkingLevelEfforts(id), func(effort string) map[string]any {
		return map[string]any{"thinkingConfig": map[string]any{"includeThoughts": true, "thinkingLevel": effort}}
	})
}

func compatSAPVariants(desc compatModelDescriptor, adaptiveEfforts []string) map[string]map[string]any {
	if strings.Contains(desc.APIID, "anthropic") {
		if len(adaptiveEfforts) > 0 {
			return compatWrapInSAPModelParams(compatVariantsFromEfforts(adaptiveEfforts, func(effort string) map[string]any {
				thinking := map[string]any{"type": "adaptive"}
				if compatAnthropicOpus47OrLater(desc.APIID) {
					thinking["display"] = "summarized"
				}
				return map[string]any{
					"thinking":      thinking,
					"output_config": map[string]any{"effort": effort},
				}
			}))
		}
		return compatWrapInSAPModelParams(map[string]map[string]any{
			"high": {"thinking": map[string]any{"type": "enabled", "budget_tokens": 16000}},
			"max":  {"thinking": map[string]any{"type": "enabled", "budget_tokens": 31999}},
		})
	}
	if strings.Contains(desc.APIID, "gemini") && strings.Contains(desc.APIID, "2.5") {
		return compatWrapInSAPModelParams(map[string]map[string]any{
			"high": {"thinkingConfig": map[string]any{"includeThoughts": true, "thinkingBudget": 16000}},
			"max":  {"thinkingConfig": map[string]any{"includeThoughts": true, "thinkingBudget": compatGoogleThinkingBudgetMax(desc.APIID)}},
		})
	}
	if strings.Contains(desc.APIID, "gpt") || compatSAPReasoningRE.MatchString(desc.APIID) {
		return compatWrapInSAPModelParams(compatVariantsFromEfforts(
			compatReasoningEfforts(desc.APIID, desc.ReleaseDate),
			func(effort string) map[string]any {
				return map[string]any{"reasoning_effort": effort}
			},
		))
	}
	return compatWrapInSAPModelParams(compatVariantsFromEfforts(compatWidelySupportedEfforts(), func(effort string) map[string]any {
		return map[string]any{"reasoning_effort": effort}
	}))
}

func compatWrapInSAPModelParams(variants map[string]map[string]any) map[string]map[string]any {
	if len(variants) == 0 {
		return nil
	}
	out := make(map[string]map[string]any, len(variants))
	for key, value := range variants {
		out[key] = map[string]any{"modelParams": value}
	}
	return out
}

func minInt(left, right int) int {
	if right < left {
		return right
	}
	return left
}
