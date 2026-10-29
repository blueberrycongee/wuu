package runtime

import (
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"testing"
)

func TestExternalReadReplacementRequiresExplicitReadOnlyContract(t *testing.T) {
	valid := pluginhost.ToolRegistration{ReplacementContracts: []string{"read_file.text.v1"}, Activity: &pluginhost.ToolActivityMetadata{ReadOnly: true}}
	if err := validateExternalReadRegistration(valid); err != nil {
		t.Fatal(err)
	}
	for _, registration := range []pluginhost.ToolRegistration{{Activity: &pluginhost.ToolActivityMetadata{ReadOnly: true}}, {ReplacementContracts: []string{"read_file.text.v1"}}, {ReplacementContracts: []string{"read_file.text.v1"}, Activity: &pluginhost.ToolActivityMetadata{ReadOnly: true, Orchestrator: true}}} {
		if err := validateExternalReadRegistration(registration); err == nil {
			t.Fatalf("accepted invalid contract: %+v", registration)
		}
	}
}
