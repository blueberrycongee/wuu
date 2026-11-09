package provideroptions

import "testing"

func TestCloneDeepCopiesNestedOptions(t *testing.T) {
	input := map[string]any{
		"disabled": true,
		"nested": map[string]any{
			"keep": "yes",
		},
		"list": []any{map[string]any{"item": "value"}},
	}

	got := Clone(input)
	input["nested"].(map[string]any)["keep"] = "changed"
	input["list"].([]any)[0].(map[string]any)["item"] = "changed"

	if got["disabled"] != true {
		t.Fatalf("Clone disabled = %#v", got["disabled"])
	}
	if got["nested"].(map[string]any)["keep"] != "yes" {
		t.Fatalf("Clone nested = %#v", got["nested"])
	}
	if got["list"].([]any)[0].(map[string]any)["item"] != "value" {
		t.Fatalf("Clone list = %#v", got["list"])
	}
}

func TestMergeWithoutDisabledDeepMergesAndFiltersDisabled(t *testing.T) {
	base := map[string]any{
		"disabled": true,
		"reasoning": map[string]any{
			"effort": "medium",
		},
	}
	override := map[string]any{
		"disabled": false,
		"reasoning": map[string]any{
			"summary": "auto",
		},
	}

	got := MergeWithoutDisabled(base, override)
	if _, ok := got["disabled"]; ok {
		t.Fatalf("MergeWithoutDisabled kept disabled: %#v", got)
	}
	reasoning := got["reasoning"].(map[string]any)
	if reasoning["effort"] != "medium" || reasoning["summary"] != "auto" {
		t.Fatalf("MergeWithoutDisabled reasoning = %#v", reasoning)
	}
}
