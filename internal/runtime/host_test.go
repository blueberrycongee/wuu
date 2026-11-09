package runtime

import "testing"

func TestResolveHostDefaultsToLocal(t *testing.T) {
	host, err := ResolveHost(Host{})
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}
	if host.Kind != HostLocal || host.InstanceID != "" {
		t.Fatalf("unexpected host: %+v", host)
	}
}

func TestResolveHostRequiresCloudInstanceID(t *testing.T) {
	if _, err := ResolveHost(Host{Kind: HostCloud}); err == nil {
		t.Fatal("expected missing cloud instance id error")
	}

	host, err := ResolveHost(Host{Kind: HostCloud, InstanceID: " run-123 "})
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}
	if host.Kind != HostCloud || host.InstanceID != "run-123" {
		t.Fatalf("unexpected host: %+v", host)
	}
}

func TestResolveHostRejectsLocalInstanceID(t *testing.T) {
	if _, err := ResolveHost(Host{Kind: HostLocal, InstanceID: "run-123"}); err == nil {
		t.Fatal("expected local instance id error")
	}
}

func TestResolveHostRejectsUnknownKind(t *testing.T) {
	if _, err := ResolveHost(Host{Kind: "desktop"}); err == nil {
		t.Fatal("expected unsupported host error")
	}
}
