package toolresult

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStructuredContentIndexJSONPreservesBoundedSemanticValues(t *testing.T) {
	secret := strings.Repeat("evidence-", 100)
	encoded := StructuredContentIndexJSON(json.RawMessage(`{"nested":{"token":"abc"},"status":"ready","count":3,"payload":"` + secret + `"}`))
	if encoded == "" {
		t.Fatal("structured index is empty")
	}
	var index map[string]any
	if err := json.Unmarshal([]byte(encoded), &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	preview, ok := index["value_preview"].(map[string]any)
	if !ok || preview["status"] != "ready" || preview["count"] != float64(3) {
		t.Fatalf("semantic preview = %#v", index["value_preview"])
	}
	if strings.Contains(encoded, secret) || index["preview_truncated"] != true {
		t.Fatalf("structured preview was not bounded: %s", encoded)
	}
	if hash, _ := index["sha256"].(string); len(hash) != 64 {
		t.Fatalf("sha256 = %q", hash)
	}
}

func TestStructuredContentIndexJSONCanonicalHashIgnoresObjectKeyOrder(t *testing.T) {
	first := StructuredContentIndexJSON(json.RawMessage(`{"b":2,"a":1}`))
	second := StructuredContentIndexJSON(json.RawMessage(`{"a":1,"b":2}`))
	var firstIndex, secondIndex map[string]any
	if err := json.Unmarshal([]byte(first), &firstIndex); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(second), &secondIndex); err != nil {
		t.Fatal(err)
	}
	if firstIndex["sha256"] != secondIndex["sha256"] {
		t.Fatalf("canonical hashes differ: %v != %v", firstIndex["sha256"], secondIndex["sha256"])
	}
}

func TestStructuredContentIndexJSONBoundsNumbersWithoutChangingTheirValue(t *testing.T) {
	const precise = json.Number("9007199254740993")
	for _, oversized := range []json.Number{
		json.Number(strings.Repeat("9", 60000)),
		json.Number("1e" + strings.Repeat("9", 60000)),
	} {
		raw, err := json.Marshal(map[string]any{"count": precise, "payload": oversized})
		if err != nil {
			t.Fatal(err)
		}
		encoded := StructuredContentIndexJSON(raw)
		if len(encoded) > 4096 {
			t.Fatalf("one numeric scalar bypassed the index budget: %d bytes", len(encoded))
		}
		decoder := json.NewDecoder(strings.NewReader(encoded))
		decoder.UseNumber()
		var index map[string]any
		if err := decoder.Decode(&index); err != nil {
			t.Fatal(err)
		}
		preview := index["value_preview"].(map[string]any)
		if preview["count"] != precise {
			t.Fatalf("representable numeric evidence lost precision: %v", preview["count"])
		}
		if _, isNumber := preview["payload"].(json.Number); isNumber || index["preview_truncated"] != true {
			t.Fatal("oversized number must be marked omitted rather than clipped into another number")
		}
	}
}
