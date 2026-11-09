package pluginhost

import "testing"

// Renderer transport reports must preserve multi-window failures, reject stale
// activation callbacks, and distinguish registration from an actual render.
func TestRendererActivationReports(t *testing.T) {
	r := NewServiceRegistry()
	r.SetComposition(CompositionSnapshot{Modules: []CompositionModuleSnapshot{{ID: "shell"}}})
	r.RegisterRenderer("shell", "source-v1")
	state := func() RendererActivationSnapshot { return *r.Snapshot(7).Composition.Modules[0].Renderer }
	if got := state().State; got != "pending" {
		t.Fatalf("unacknowledged renderer = %q", got)
	}
	report := func(window string, activation uint64, phase, status string) {
		t.Helper()
		if err := r.ReportRenderer("shell", "source-v1", window, RendererActivationReport{Activation: activation, Phase: phase, Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	report("one", 1, "registration", "active")
	if got := state().State; got != "registered" {
		t.Fatalf("registration = %q", got)
	}
	report("one", 1, "render", "failed")
	report("two", 1, "registration", "active")
	report("two", 1, "render", "active")
	report("one", 1, "registration", "active")
	if got := state().State; got != "failed" {
		t.Fatalf("one window hid failure: %q", got)
	}
	report("one", 2, "registration", "active")
	report("one", 2, "render", "active")
	if err := r.ReportRenderer("shell", "source-v1", "one", RendererActivationReport{Activation: 1, Phase: "cleanup", Status: "disposed"}); err == nil {
		t.Fatal("accepted late old cleanup")
	}
	if got := state().State; got != "active" {
		t.Fatalf("recovered renderer = %q", got)
	}
	if err := r.ReportRenderer("shell", "other-source", "one", RendererActivationReport{Activation: 3, Phase: "registration", Status: "active"}); err == nil {
		t.Fatal("accepted another source")
	}
	r.ForgetRenderer("one")
	if len(state().Clients) != 1 {
		t.Fatal("disconnect retained renderer")
	}
	r.ForgetRenderer("two")
	if got := state().State; got != "pending" {
		t.Fatalf("disconnected renderer = %q", got)
	}
}
