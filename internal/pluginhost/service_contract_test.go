package pluginhost

import (
	"strings"
	"testing"
)

func validServiceDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name:    "search.provider",
		Version: "1.2.0",
		Methods: []ServiceMethodDescriptor{
			{Name: "query", InputSchema: "search.query.request.v1", OutputSchema: "search.query.response.v1"},
		},
	}
}

func TestValidateServiceDescriptor(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*ServiceDescriptor)
		wantErrSub string
	}{
		{name: "valid", mutate: func(*ServiceDescriptor) {}},
		{name: "underscored method", mutate: func(d *ServiceDescriptor) { d.Methods[0].Name = "history_fork" }},
		{name: "PascalCase method and schema", mutate: func(d *ServiceDescriptor) {
			d.Methods[0] = ServiceMethodDescriptor{Name: "ReadControl", InputSchema: "session.domain.ReadControl.input.v1", OutputSchema: "session.domain.ReadControl.output.v1"}
		}},
		{name: "qualified PascalCase method", mutate: func(d *ServiceDescriptor) { d.Methods[0].Name = "Journal.Close" }},
		{name: "empty method segment", mutate: func(d *ServiceDescriptor) { d.Methods[0].Name = "Journal..Close" }, wantErrSub: "method name"},
		{name: "method path is not identifier", mutate: func(d *ServiceDescriptor) { d.Methods[0].Name = "Journal/Close" }, wantErrSub: "method name"},
		{name: "method whitespace", mutate: func(d *ServiceDescriptor) { d.Methods[0].Name = "Read Control" }, wantErrSub: "method name"},
		{name: "name must be dotted", mutate: func(d *ServiceDescriptor) { d.Name = "search" }, wantErrSub: "dotted"},
		{name: "name must be lowercase", mutate: func(d *ServiceDescriptor) { d.Name = "Search.provider" }, wantErrSub: "dotted lowercase"},
		{name: "version must be semver", mutate: func(d *ServiceDescriptor) { d.Version = "1.2" }, wantErrSub: "semver"},
		{name: "version rejects prerelease", mutate: func(d *ServiceDescriptor) { d.Version = "1.2.0-rc.1" }, wantErrSub: "semver"},
		{name: "version rejects leading zeros", mutate: func(d *ServiceDescriptor) { d.Version = "01.2.0" }, wantErrSub: "semver"},
		{name: "methods required", mutate: func(d *ServiceDescriptor) { d.Methods = nil }, wantErrSub: "at least one method"},
		{
			name: "duplicate methods",
			mutate: func(d *ServiceDescriptor) {
				d.Methods = append(d.Methods, ServiceMethodDescriptor{Name: "query", InputSchema: "search.query.request.v2", OutputSchema: "search.query.response.v2"})
			},
			wantErrSub: "duplicate method",
		},
		{
			name: "schema ids must be dotted",
			mutate: func(d *ServiceDescriptor) {
				d.Methods[0].InputSchema = "request"
			},
			wantErrSub: "dotted versioned",
		},
		{
			name: "method names may be dotted",
			mutate: func(d *ServiceDescriptor) {
				d.Methods[0].Name = "query.stream"
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			descriptor := validServiceDescriptor()
			tt.mutate(&descriptor)
			err := ValidateServiceDescriptor(descriptor)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("ValidateServiceDescriptor() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("ValidateServiceDescriptor() = %v, want error containing %q", err, tt.wantErrSub)
			}
		})
	}
}

func TestServiceVersionMajor(t *testing.T) {
	if major, ok := ServiceVersionMajor("2.10.3"); !ok || major != 2 {
		t.Fatalf("ServiceVersionMajor(2.10.3) = %d, %v", major, ok)
	}
	if _, ok := ServiceVersionMajor("2.x.3"); ok {
		t.Fatal("ServiceVersionMajor accepted a non-semver string")
	}
}

func TestValidateServiceNegotiation(t *testing.T) {
	tests := []struct {
		name       string
		result     CapabilityInitializeResult
		wantErrSub string
	}{
		{name: "empty is always legal", result: CapabilityInitializeResult{ProtocolVersion: 1}},
		{
			name: "v3 provide and consume",
			result: CapabilityInitializeResult{
				ProtocolVersion:  3,
				ProvidedServices: []ServiceDescriptor{validServiceDescriptor()},
				RequiredServices: []ServiceRequirement{{Name: "memory.index", MajorVersion: 1, Required: true}},
			},
		},
		{
			name: "v2 cannot declare services",
			result: CapabilityInitializeResult{
				ProtocolVersion:  2,
				ProvidedServices: []ServiceDescriptor{validServiceDescriptor()},
			},
			wantErrSub: "protocol v3",
		},
		{
			name: "v1 cannot declare services",
			result: CapabilityInitializeResult{
				ProtocolVersion:  1,
				RequiredServices: []ServiceRequirement{{Name: "memory.index", MajorVersion: 1}},
			},
			wantErrSub: "protocol v3",
		},
		{
			name: "duplicate provided major",
			result: CapabilityInitializeResult{
				ProtocolVersion: 3,
				ProvidedServices: []ServiceDescriptor{
					validServiceDescriptor(),
					{Name: "search.provider", Version: "1.3.0", Methods: []ServiceMethodDescriptor{{Name: "query", InputSchema: "search.query.request.v2", OutputSchema: "search.query.response.v2"}}},
				},
			},
			wantErrSub: "duplicate provided service",
		},
		{
			name: "same name different major is legal",
			result: CapabilityInitializeResult{
				ProtocolVersion: 3,
				ProvidedServices: []ServiceDescriptor{
					validServiceDescriptor(),
					{Name: "search.provider", Version: "2.0.0", Methods: []ServiceMethodDescriptor{{Name: "query", InputSchema: "search.query.request.v2", OutputSchema: "search.query.response.v2"}}},
				},
			},
		},
		{
			name: "duplicate requirement",
			result: CapabilityInitializeResult{
				ProtocolVersion: 3,
				RequiredServices: []ServiceRequirement{
					{Name: "memory.index", MajorVersion: 1},
					{Name: "memory.index", MajorVersion: 2},
				},
			},
			wantErrSub: "duplicate required service",
		},
		{
			name: "provide and require same name",
			result: CapabilityInitializeResult{
				ProtocolVersion:  3,
				ProvidedServices: []ServiceDescriptor{validServiceDescriptor()},
				RequiredServices: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1}},
			},
			wantErrSub: "both provided and required",
		},
		{
			name: "negative major",
			result: CapabilityInitializeResult{
				ProtocolVersion:  3,
				RequiredServices: []ServiceRequirement{{Name: "memory.index", MajorVersion: -1}},
			},
			wantErrSub: "cannot be negative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateServiceNegotiation(tt.result)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("ValidateServiceNegotiation() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("ValidateServiceNegotiation() = %v, want error containing %q", err, tt.wantErrSub)
			}
		})
	}
}

func TestValidateCapabilityNegotiationServiceGates(t *testing.T) {
	v3Plugin := CapabilityInitializeResult{
		ProtocolVersion:  3,
		ProvidedServices: []ServiceDescriptor{validServiceDescriptor()},
	}
	if err := ValidateCapabilityNegotiation(v3Plugin, []HostServiceMethod{ServiceCallMethod}); err != nil {
		t.Fatalf("v3 service plugin rejected: %v", err)
	}
	future := CapabilityInitializeResult{
		ProtocolVersion:  4,
		ProvidedServices: []ServiceDescriptor{validServiceDescriptor()},
	}
	if err := ValidateCapabilityNegotiation(future, []HostServiceMethod{ServiceCallMethod}); err == nil {
		t.Fatal("protocol v4 must be rejected")
	}
}
