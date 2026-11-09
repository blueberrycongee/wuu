package insight

import (
	"encoding/json"
	"testing"
)

func TestSkillUsageJSONContract(t *testing.T) {
	raw, err := json.Marshal(SkillUsage{Name: "commit", Count: 3})
	if err != nil {
		t.Fatalf("marshal skill usage: %v", err)
	}
	if got, want := string(raw), `{"name":"commit","count":3}`; got != want {
		t.Fatalf("SkillUsage JSON = %s, want %s", got, want)
	}
}
