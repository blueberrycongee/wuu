// Package grokbuildspec contains the stable client-facing defaults shared by
// Grok Build configuration and transport code.
package grokbuildspec

const (
	DefaultBaseURL       = "https://cli-chat-proxy.grok.com/v1"
	DefaultModel         = "grok-4.5"
	ContextWindowTokens  = 500_000
	OAuthCredentialScope = "https://auth.x.ai::b1a00492-073a-47ea-816f-4c329264a828"
	// CredentialIssuer is the legacy scope retained as a fallback by Grok Build.
	CredentialIssuer          = "https://accounts.x.ai/sign-in"
	TokenAuthHeaderValue      = "xai-grok-cli"
	ClientVersion             = "1.0.13"
	ClientIdentifier          = "wuu"
	ClientMode                = "interactive"
	AuthenticateResponseValue = "authenticate-response"
)

type Model struct {
	ID            string
	DisplayName   string
	Efforts       []string
	DefaultEffort string
}

// Models matches the model list and defaults advertised by Grok Build 1.0.13.
// xAI documents the same 500k context window and reasoning tiers for these
// model IDs.
var Models = []Model{
	{
		ID:            "grok-4.5",
		DisplayName:   "Grok 4.5",
		Efforts:       []string{"low", "medium", "high"},
		DefaultEffort: "high",
	},
	{
		ID:            "grok-4.6",
		DisplayName:   "Grok 4.6",
		Efforts:       []string{"low", "medium", "high", "xhigh"},
		DefaultEffort: "high",
	},
	{
		ID:            "grok-4.7",
		DisplayName:   "Grok 4.7",
		Efforts:       []string{"low", "medium", "high", "xhigh"},
		DefaultEffort: "high",
	},
}
