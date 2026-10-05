package modelprofile

import (
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type Family string

const (
	FamilyPortable Family = "portable"
	FamilyClaude   Family = "claude"
	FamilyCodex    Family = "codex"
	FamilyGPT      Family = "gpt"
	FamilyGemini   Family = "gemini"
	FamilyKimi     Family = "kimi"
	FamilyDeepSeek Family = "deepseek"
	FamilyQwen     Family = "qwen"
	FamilyLocal    Family = "local"
)

type ToolCallingMode string

const (
	ToolCallingNone           ToolCallingMode = "none"
	ToolCallingJSON           ToolCallingMode = "json"
	ToolCallingStrictJSON     ToolCallingMode = "strict_json"
	ToolCallingProviderNative ToolCallingMode = "provider_native"
)

type CacheGranularity string

const (
	CacheGranularityNone             CacheGranularity = "none"
	CacheGranularityMessage          CacheGranularity = "message"
	CacheGranularityBlock            CacheGranularity = "block"
	CacheGranularityProviderSpecific CacheGranularity = "provider_specific"
)

type ReasoningBudget string

const (
	ReasoningBudgetNone   ReasoningBudget = "none"
	ReasoningBudgetLow    ReasoningBudget = "low"
	ReasoningBudgetMedium ReasoningBudget = "medium"
	ReasoningBudgetHigh   ReasoningBudget = "high"
	ReasoningBudgetXHigh  ReasoningBudget = "xhigh"
)

type WriteMode string

const (
	WriteModeExactEdit WriteMode = "exact_edit"
)

type LatencyClass string

const (
	LatencyClassFast   LatencyClass = "fast"
	LatencyClassNormal LatencyClass = "normal"
	LatencyClassSlow   LatencyClass = "slow"
)

type Profile struct {
	ProviderName string
	Model        string
	Family       Family
	APIShape     APIShape
	Context      Context
	Reasoning    Reasoning
	Code         Code
	Execution    Execution
	Economics    Economics
}

type APIShape struct {
	Chat              bool
	Responses         bool
	ToolCalling       ToolCallingMode
	FreeformTool      bool
	ParallelToolCalls bool
	StreamingToolArgs bool
	SystemRole        bool
	DeveloperRole     bool
}

type Context struct {
	WindowTokens        int
	MaxOutputTokens     int
	SupportsPromptCache bool
	CacheGranularity    CacheGranularity
}

type Reasoning struct {
	HiddenReasoning      bool
	VisibleReasoning     bool
	Budget               ReasoningBudget
	PrefersExplicitPlan  bool
	LongHorizonScore     int
	CompactActionLoop    bool
	VerboseToolRationale bool
}

type Code struct {
	ExactEditReliability   int
	WholeFileReliability   int
	PathHallucinationRisk  int
	TestDebugScore         int
	PreferredEditPrimitive WriteMode
}

type Execution struct {
	DefaultMaxAutonomousSteps int
	DefaultWriteMode          WriteMode
	DefaultSearchBudget       int
	NeedsReadBeforeWrite      bool
	AllowParallelReadOnly     bool
	AllowDirectShell          bool
}

type Economics struct {
	InputCost         float64
	OutputCost        float64
	Latency           LatencyClass
	DailyBudgetPolicy string
}

func Resolve(providerName, model string) Profile {
	providerName = strings.TrimSpace(providerName)
	model = strings.TrimSpace(model)
	family := FamilyForProviderModel(providerName, model)
	profile := baseProfile(providerName, model, family)

	switch family {
	case FamilyClaude:
		applyClaude(&profile)
	case FamilyCodex:
		applyCodex(&profile)
	case FamilyGPT:
		applyGPT(&profile)
	case FamilyGemini:
		applyGemini(&profile)
	case FamilyKimi, FamilyDeepSeek, FamilyQwen:
		applyStrictPortableCoder(&profile)
	case FamilyLocal:
		applyLocal(&profile)
	}

	return profile
}

func FamilyForProviderModel(providerName, model string) Family {
	id := strings.ToLower(strings.TrimSpace(providerName + "/" + model))
	provider := strings.ToLower(strings.TrimSpace(providerName))
	switch {
	case strings.Contains(id, "claude") || strings.Contains(id, "anthropic"):
		return FamilyClaude
	case strings.Contains(id, "codex"):
		return FamilyCodex
	case strings.Contains(id, "gpt") || strings.Contains(id, "openai"):
		return FamilyGPT
	case strings.Contains(id, "gemini") || strings.Contains(id, "google"):
		return FamilyGemini
	case strings.Contains(id, "kimi") || strings.Contains(id, "moonshot"):
		return FamilyKimi
	case strings.Contains(id, "deepseek"):
		return FamilyDeepSeek
	case strings.Contains(id, "qwen") || strings.Contains(id, "dashscope"):
		return FamilyQwen
	case provider == "local" || strings.Contains(provider, "ollama") || strings.Contains(provider, "lmstudio"):
		return FamilyLocal
	default:
		return FamilyPortable
	}
}

func baseProfile(providerName, model string, family Family) Profile {
	window, _ := providers.KnownContextWindowFor(model)
	return Profile{
		ProviderName: providerName,
		Model:        model,
		Family:       family,
		APIShape: APIShape{
			Chat:              true,
			ToolCalling:       ToolCallingJSON,
			ParallelToolCalls: false,
			SystemRole:        true,
		},
		Context: Context{
			WindowTokens:     window,
			CacheGranularity: CacheGranularityNone,
		},
		Reasoning: Reasoning{
			Budget:              ReasoningBudgetMedium,
			PrefersExplicitPlan: false,
			LongHorizonScore:    2,
			CompactActionLoop:   true,
		},
		Code: Code{
			ExactEditReliability:   3,
			WholeFileReliability:   2,
			PathHallucinationRisk:  3,
			TestDebugScore:         3,
			PreferredEditPrimitive: WriteModeExactEdit,
		},
		Execution: Execution{
			DefaultMaxAutonomousSteps: 20,
			DefaultWriteMode:          WriteModeExactEdit,
			DefaultSearchBudget:       8,
			NeedsReadBeforeWrite:      true,
			AllowDirectShell:          true,
		},
		Economics: Economics{
			Latency: LatencyClassNormal,
		},
	}
}

func applyClaude(profile *Profile) {
	profile.APIShape.ToolCalling = ToolCallingProviderNative
	profile.APIShape.ParallelToolCalls = true
	profile.Context.SupportsPromptCache = true
	profile.Context.CacheGranularity = CacheGranularityBlock
	profile.Reasoning.HiddenReasoning = true
	profile.Reasoning.VisibleReasoning = true
	profile.Reasoning.PrefersExplicitPlan = true
	profile.Reasoning.LongHorizonScore = 4
	profile.Reasoning.VerboseToolRationale = true
	profile.Code.ExactEditReliability = 5
	profile.Code.TestDebugScore = 4
	profile.Execution.DefaultMaxAutonomousSteps = 18
	profile.Execution.DefaultSearchBudget = 10
	profile.Execution.AllowParallelReadOnly = true
}

func applyCodex(profile *Profile) {
	profile.APIShape.Responses = true
	profile.APIShape.ToolCalling = ToolCallingProviderNative
	profile.APIShape.FreeformTool = true
	profile.APIShape.ParallelToolCalls = true
	profile.APIShape.StreamingToolArgs = true
	profile.APIShape.DeveloperRole = true
	profile.Reasoning.HiddenReasoning = true
	profile.Reasoning.Budget = ReasoningBudgetHigh
	profile.Reasoning.LongHorizonScore = 5
	profile.Code.ExactEditReliability = 4
	profile.Code.PathHallucinationRisk = 2
	profile.Code.TestDebugScore = 5
	profile.Execution.DefaultMaxAutonomousSteps = 30
	profile.Execution.DefaultSearchBudget = 8
	profile.Execution.AllowParallelReadOnly = true
}

func applyGPT(profile *Profile) {
	applyCodex(profile)
	profile.Family = FamilyGPT
	profile.Reasoning.Budget = ReasoningBudgetMedium
	profile.Reasoning.LongHorizonScore = 4
	profile.Execution.DefaultMaxAutonomousSteps = 24
}

func applyGemini(profile *Profile) {
	profile.APIShape.ToolCalling = ToolCallingStrictJSON
	profile.APIShape.ParallelToolCalls = true
	profile.Reasoning.HiddenReasoning = true
	profile.Reasoning.PrefersExplicitPlan = true
	profile.Reasoning.LongHorizonScore = 3
	profile.Code.ExactEditReliability = 4
	profile.Code.PathHallucinationRisk = 3
	profile.Execution.DefaultSearchBudget = 6
}

func applyStrictPortableCoder(profile *Profile) {
	profile.APIShape.ToolCalling = ToolCallingStrictJSON
	profile.Reasoning.PrefersExplicitPlan = true
	profile.Code.ExactEditReliability = 4
	profile.Code.PathHallucinationRisk = 4
	profile.Code.TestDebugScore = 3
	profile.Execution.DefaultMaxAutonomousSteps = 12
	profile.Execution.DefaultSearchBudget = 5
}

func applyLocal(profile *Profile) {
	profile.APIShape.ToolCalling = ToolCallingStrictJSON
	profile.APIShape.ParallelToolCalls = false
	profile.APIShape.FreeformTool = false
	profile.Reasoning.Budget = ReasoningBudgetLow
	profile.Reasoning.PrefersExplicitPlan = true
	profile.Reasoning.LongHorizonScore = 1
	profile.Code.ExactEditReliability = 2
	profile.Code.WholeFileReliability = 1
	profile.Code.PathHallucinationRisk = 5
	profile.Code.TestDebugScore = 2
	profile.Execution.DefaultMaxAutonomousSteps = 5
	profile.Execution.DefaultSearchBudget = 3
	profile.Execution.AllowDirectShell = false
}
