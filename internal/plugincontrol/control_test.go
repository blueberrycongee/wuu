package plugincontrol

import (
	"context"
	"testing"
)

func TestLiveHostsReportIndependentFailuresAndRequireAuthorization(t *testing.T) {
	home := t.TempDir()
	for _, domain := range []string{"coding", "personal"} {
		state := "active"
		if domain == "personal" {
			state = "failed"
		}
		host, err := Register(home, HostInfo{ProductDomain: domain}, func(_ context.Context, request Request) Response {
			return Response{Plugins: []PluginStatus{{ID: "fixture", Fingerprint: "published", State: state}}}
		})
		if err != nil {
			t.Fatal(err)
		}
		defer host.Close()
	}
	reports, err := Inspect(context.Background(), home, "fixture")
	if err != nil || len(reports) != 2 {
		t.Fatalf("reports=%+v err=%v", reports, err)
	}
	for _, report := range reports {
		if report.Host.ProductDomain == "personal" && report.Plugins[0].State != "failed" {
			t.Fatal("coding host hid personal failure")
		}
	}
	endpoints, err := List(home)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := endpoints[0]
	endpoint.Token = "wrong"
	if _, err := Call(context.Background(), endpoint, Request{Operation: "status"}); err == nil {
		t.Fatal("unauthorized host access")
	}
	endpoint.Address = "https://example.com"
	if _, err := Call(context.Background(), endpoint, Request{Operation: "status"}); err == nil {
		t.Fatal("non-local endpoint accepted")
	}
}

func TestHostRegistrationLifetimeUsesLockOwnership(t *testing.T) {
	home := t.TempDir()
	host, err := Register(home, HostInfo{}, func(context.Context, Request) Response { return Response{} })
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	endpoints, err := List(home)
	if err != nil || len(endpoints) != 1 {
		t.Fatalf("live hosts: %+v %v", endpoints, err)
	}
	// Model a process exit leaving its registry file: no timestamp or PID reuse
	// can make an unlocked registration count as a live host.
	if err := host.lease.Release(); err != nil {
		t.Fatal(err)
	}
	endpoints, err = List(home)
	if err != nil || len(endpoints) != 0 {
		t.Fatalf("dead host reported live: %+v %v", endpoints, err)
	}
}
