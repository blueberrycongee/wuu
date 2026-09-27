package pluginhost

import (
	"strings"
	"testing"
)

func TestValidateCapabilityDescriptor(t *testing.T) {
	tests := []struct {
		name    string
		desc    CapabilityDescriptor
		wantErr bool
	}{
		{
			name:    "empty id",
			desc:    CapabilityDescriptor{Kind: "transform", Version: 1},
			wantErr: true,
		},
		{
			name:    "no dot in id",
			desc:    CapabilityDescriptor{ID: "tool", Kind: "transform", Version: 1},
			wantErr: true,
		},
		{
			name:    "host-only capability",
			desc:    CapabilityDescriptor{ID: "host.plugin.install", Kind: SeamDecision, Version: 1},
			wantErr: true,
		},
		{
			name:    "empty kind",
			desc:    CapabilityDescriptor{ID: "agent.tool.execute", Version: 1},
			wantErr: true,
		},
		{
			name:    "unknown kind",
			desc:    CapabilityDescriptor{ID: "agent.tool.execute", Kind: "unknown", Version: 1},
			wantErr: true,
		},
		{
			name:    "zero version",
			desc:    CapabilityDescriptor{ID: "agent.tool.execute", Kind: "transform", Version: 0},
			wantErr: true,
		},
		{
			name:    "valid observe",
			desc:    CapabilityDescriptor{ID: "agent.session.lifecycle", Kind: "observe", ErrorPolicy: ErrorPolicyIsolate, Version: 1},
			wantErr: false,
		},
		{
			name:    "valid transform",
			desc:    CapabilityDescriptor{ID: "agent.tool.execute.after", Kind: "transform", Version: 2, Priority: 10},
			wantErr: false,
		},
		{
			name:    "guard is not implemented",
			desc:    CapabilityDescriptor{ID: "agent.permission.policy", Kind: "guard", Version: 1},
			wantErr: true,
		},
		{
			name:    "around is not implemented",
			desc:    CapabilityDescriptor{ID: "agent.tool.execute.around", Kind: "around", Version: 1},
			wantErr: true,
		},
		{
			name:    "valid decision",
			desc:    CapabilityDescriptor{ID: "agent.compaction", Kind: "decision", Version: 1},
			wantErr: false,
		},
		{
			name:    "system prompt requires transform v1",
			desc:    CapabilityDescriptor{ID: CapabilityAgentSystemPromptSection, Kind: "observe", Version: 1},
			wantErr: true,
		},
		{
			name:    "compaction note fork supports decision v2",
			desc:    CapabilityDescriptor{ID: CapabilityAgentCompaction, Kind: "decision", Version: 2},
			wantErr: false,
		},
		{
			name:    "summary-free context windows support decision v3",
			desc:    CapabilityDescriptor{ID: CapabilityAgentCompaction, Kind: "decision", Version: 3},
			wantErr: false,
		},
		{
			name:    "compaction rejects unsupported v4",
			desc:    CapabilityDescriptor{ID: CapabilityAgentCompaction, Kind: "decision", Version: 4},
			wantErr: true,
		},
		{
			name:    "observe cannot propagate",
			desc:    CapabilityDescriptor{ID: "agent.observe.custom", Kind: SeamObserve, ErrorPolicy: ErrorPolicyPropagate, Version: 1},
			wantErr: true,
		},
		{
			name:    "ignore only valid for observe",
			desc:    CapabilityDescriptor{ID: "agent.transform.custom", Kind: SeamTransform, ErrorPolicy: ErrorPolicyIgnore, Version: 1},
			wantErr: true,
		},
		{
			name:    "turn completed defaults to isolate",
			desc:    CapabilityDescriptor{ID: CapabilityAgentTurnCompleted, Kind: SeamObserve, Version: 1},
			wantErr: false,
		},
		{
			name:    "turn interrupted defaults to isolate",
			desc:    CapabilityDescriptor{ID: CapabilityAgentTurnInterrupted, Kind: SeamObserve, Version: 1},
			wantErr: false,
		},
		{
			name: "with dependencies",
			desc: CapabilityDescriptor{
				ID: "agent.tool.custom", Kind: "transform", Version: 1,
				DependsOn: []string{"agent.tool.register"},
			},
			wantErr: false,
		},
		{
			name: "with conflicts",
			desc: CapabilityDescriptor{
				ID: "agent.compaction.custom", Kind: "decision", Version: 1,
				Conflicts: []string{"agent.compaction.default"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCapabilityDescriptor(tt.desc)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCapabilityDescriptor() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateCapabilityNegotiationHostServices(t *testing.T) {
	base := CapabilityInitializeResult{ProtocolVersion: CapabilityProtocolVersion}
	base.Capabilities = []CapabilityDescriptor{
		{ID: CapabilityAgentRequestTransform, Kind: "transform", Version: 1},
		{ID: CapabilityAgentSystemPromptSection, Kind: "transform", Version: 1},
		{ID: CapabilityAgentCompaction, Kind: "decision", Version: 1},
		{ID: CapabilityPluginClientRequest, Kind: "decision", Version: 1},
	}

	optional := base
	optional.RequiredHostServices = []HostServiceDescriptor{{ID: "host.future.optional"}}
	if err := ValidateCapabilityNegotiation(optional, nil); err != nil {
		t.Fatalf("optional unsupported service rejected: %v", err)
	}

	required := base
	required.RequiredHostServices = []HostServiceDescriptor{{ID: string(ServiceCallMethod), Required: true}}
	if err := ValidateCapabilityNegotiation(required, nil); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("required unavailable service error = %v", err)
	}
	if err := ValidateCapabilityNegotiation(required, []HostServiceMethod{ServiceCallMethod}); err != nil {
		t.Fatalf("available required service rejected: %v", err)
	}
}

func TestValidateCapabilityNegotiationRejectsUnsupportedAndDuplicateDeclarations(t *testing.T) {
	tests := []struct {
		name   string
		result CapabilityInitializeResult
		match  string
	}{
		{
			name: "unsupported capability",
			result: CapabilityInitializeResult{ProtocolVersion: 2, Capabilities: []CapabilityDescriptor{{
				ID: "agent.future.transform", Kind: "transform", Version: 1,
			}}},
			match: "not supported",
		},
		{
			name: "duplicate capability",
			result: CapabilityInitializeResult{ProtocolVersion: 2, Capabilities: []CapabilityDescriptor{
				{ID: CapabilityAgentRequestTransform, Kind: "transform", Version: 1},
				{ID: CapabilityAgentRequestTransform, Kind: "transform", Version: 1},
			}},
			match: "duplicate capability",
		},
		{
			name: "v1 declaration",
			result: CapabilityInitializeResult{ProtocolVersion: 1, Capabilities: []CapabilityDescriptor{{
				ID: CapabilityAgentRequestTransform, Kind: "transform", Version: 1,
			}}},
			match: "protocol v1",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateCapabilityNegotiation(test.result, nil)
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("error = %v, want %q", err, test.match)
			}
		})
	}
}
