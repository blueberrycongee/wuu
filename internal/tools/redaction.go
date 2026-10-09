package tools

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
)

var toolOutputRedactors = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|refresh[_-]?token|id[_-]?token|token|authorization|password|passwd|secret)\s*[:=]\s*["']?[^"'\s,;]+`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`),
	// PEM-encoded private keys (SSH, TLS): the base64 body contains no
	// key=value marker, so it needs a dedicated block pattern.
	regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`),
}

var (
	toolOutputKeyValueSep = regexp.MustCompile(`\s*[:=]\s*`)
	toolOutputBearer      = regexp.MustCompile(`(?i)^bearer\s+`)
	toolOutputJWT         = regexp.MustCompile(`\b[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)
)

func redactToolOutput(value string) string {
	if value == "" {
		return value
	}
	out := value
	for _, re := range toolOutputRedactors {
		out = re.ReplaceAllStringFunc(out, redactToolOutputMatch)
	}
	return toolOutputJWT.ReplaceAllStringFunc(out, func(candidate string) string {
		// Dotted module and test names also match the compact-token shape.
		// A JOSE header distinguishes credentials from ordinary identifiers.
		encoded, _, _ := strings.Cut(candidate, ".")
		raw, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			return candidate
		}
		var header struct {
			Algorithm string `json:"alg"`
		}
		if json.Unmarshal(raw, &header) != nil || header.Algorithm == "" {
			return candidate
		}
		return "[REDACTED]"
	})
}

// RedactToolOutput masks common credential patterns in user-visible tool text.
func RedactToolOutput(value string) string {
	return redactToolOutput(value)
}

func redactToolOutputMatch(match string) string {
	if loc := toolOutputKeyValueSep.FindStringIndex(match); loc != nil {
		sep := match[loc[0]:loc[1]]
		return match[:loc[0]] + sep + "[REDACTED]"
	}
	if bearer := toolOutputBearer.FindString(match); bearer != "" {
		return bearer + "[REDACTED]"
	}
	return "[REDACTED]"
}
