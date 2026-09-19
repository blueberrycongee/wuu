package contextbudget

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestEstimateImageTokensUsesVisualPatchBudget(t *testing.T) {
	image := providers.InputImage{
		MediaType: "image/png",
		Data:      strings.Repeat("a", 1_000_000),
		Width:     2048,
		Height:    2048,
	}

	if got, want := EstimateImageTokens(image), 4096; got != want {
		t.Fatalf("image tokens should use patch budget, got %d, want %d", got, want)
	}
}

func TestEstimateImageTokensCapsAtImageBudget(t *testing.T) {
	image := providers.InputImage{
		MediaType: "image/png",
		Data:      strings.Repeat("a", 1_000_000),
		Width:     4096,
		Height:    4096,
	}

	if got, want := EstimateImageTokens(image), 10_000; got != want {
		t.Fatalf("image tokens should cap at normalized image budget, got %d, want %d", got, want)
	}
}

func TestEstimateImageTokensDecodesLegacyDimensions(t *testing.T) {
	data := encodeBudgetTestPNG(t, 33, 65)
	image := providers.InputImage{
		MediaType: "image/png",
		Data:      base64.StdEncoding.EncodeToString(data),
	}

	if got, want := EstimateImageTokens(image), 6; got != want {
		t.Fatalf("legacy image tokens should decode dimensions, got %d, want %d", got, want)
	}
}

func TestEstimateMessagesTokensDoesNotCountImageTransportBytes(t *testing.T) {
	messages := []providers.ChatMessage{{
		Role: "user",
		Images: []providers.InputImage{{
			MediaType: "image/png",
			Data:      strings.Repeat("a", 1_000_000),
			Width:     2048,
			Height:    2048,
		}},
	}}

	if got := EstimateMessagesTokens(messages); got > 5000 {
		t.Fatalf("message estimate should ignore image transport byte size, got %d", got)
	}
}

func encodeBudgetTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0x80, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// TestEstimateTokensCalibratedCoefficients locks the 2026-07-06 calibration:
// CJK at 0.7 tokens/char (real ~0.61, slight overcount by convention) and
// JSON ASCII at 10/28 chars (~2.8 chars/token). The former CJK /2
// under-estimated Chinese by ~20% (delays compaction); the former JSON /2
// over-estimated MiniMax tool arguments 1.5-2x (premature compaction), while
// /3 later under-estimated grok-4.6 JSON tool results by ~30%.
func TestEstimateTokensCalibratedCoefficients(t *testing.T) {
	cjk := strings.Repeat("上下文压缩阈值标定", 10) // 90 CJK runes
	if got := EstimateTokens(cjk); got != (90*7)/10+1 {
		t.Fatalf("CJK estimate = %d, want %d", got, (90*7)/10+1)
	}
	ascii := strings.Repeat("abcd", 25) // 100 ASCII runes
	if got := EstimateTokens(ascii); got != 100/4+1 {
		t.Fatalf("ASCII estimate = %d, want %d", got, 100/4+1)
	}
	jsonPayload := strings.Repeat(`{"k":1}`, 30) // 210 runes
	if got, want := EstimateJSONTokens(jsonPayload), 210*JSONNonCJKTokenNumerator/JSONNonCJKTokenDenominator+1; got != want {
		t.Fatalf("JSON estimate = %d, want %d", got, want)
	}
}

func TestEstimateMessagesTokensCountsToolResultsAsJSON(t *testing.T) {
	payload := "[" + strings.TrimSuffix(strings.Repeat(`{"id":"msg-1","body":"ok"},`, 40), ",") + "]"
	prose := EstimateTokens(payload)
	jsonTokens := EstimateJSONTokens(payload)
	if jsonTokens <= prose {
		t.Fatalf("JSON estimator should be denser than prose, json=%d prose=%d", jsonTokens, prose)
	}

	got := EstimateMessagesTokens([]providers.ChatMessage{{
		Role:    "tool",
		Name:    "chat_read",
		Content: payload,
		ToolResult: &toolresult.Result{
			Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: payload}},
		},
	}})
	if got != jsonTokens+4 {
		t.Fatalf("tool result estimate = %d, want JSON estimator %d plus message overhead", got, jsonTokens)
	}

	logPayload := "bash: command not found\n" + strings.Repeat("ok ", 80)
	logTokens := EstimateJSONTokens(logPayload)
	gotLog := EstimateMessagesTokens([]providers.ChatMessage{{
		Role:    "tool",
		Name:    "bash",
		Content: logPayload,
	}})
	if gotLog != logTokens+4 {
		t.Fatalf("non-JSON tool result estimate = %d, want JSON estimator %d plus message overhead", gotLog, logTokens)
	}

	userJSON := EstimateMessagesTokens([]providers.ChatMessage{{
		Role:    "user",
		Content: payload,
	}})
	if userJSON != prose+4 {
		t.Fatalf("user message estimate = %d, want prose estimator %d", userJSON, prose+4)
	}
}
