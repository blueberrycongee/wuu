package agentengine

import (
	"context"
	"errors"
	"testing"
)

type fakeFactory struct {
	id   EngineID
	desc Descriptor
}

func (f fakeFactory) Descriptor(context.Context) (Descriptor, error) {
	return f.desc, nil
}

func (f fakeFactory) Open(context.Context, OpenRequest) (Session, error) {
	return nil, nil
}

func (f fakeFactory) Resume(context.Context, ResumeRequest) (Session, error) {
	return nil, nil
}

func TestNormalizeEngineID(t *testing.T) {
	cases := map[string]EngineID{
		"":        EngineWuu,
		"   ":     EngineWuu,
		"wuu":     EngineWuu,
		"claude":  EngineID("claude"),
		" codex ": EngineID("codex"),
	}
	for input, want := range cases {
		if got := NormalizeEngineID(input); got != want {
			t.Errorf("NormalizeEngineID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIsKnownEngine(t *testing.T) {
	if !IsKnownEngine(EngineWuu) {
		t.Error("built-in wuu engine should be known")
	}
	if IsKnownEngine(EngineID("claude")) {
		t.Error("claude engine is not hostable in this build yet")
	}
	if err := CheckEngine(EngineWuu); err != nil {
		t.Errorf("CheckEngine(wuu) = %v, want nil", err)
	}
	err := CheckEngine(EngineID("codex"))
	if !errors.Is(err, ErrUnknownEngine) {
		t.Errorf("CheckEngine(codex) = %v, want ErrUnknownEngine", err)
	}
}

func TestRegistry(t *testing.T) {
	reg := NewRegistry()
	if _, ok := reg.Lookup(EngineWuu); ok {
		t.Fatal("fresh registry must be empty")
	}
	wuu := fakeFactory{id: EngineWuu, desc: Descriptor{ID: EngineWuu, Version: "1"}}
	if err := reg.Register(wuu); err != nil {
		t.Fatalf("register wuu: %v", err)
	}
	f, ok := reg.Lookup(EngineWuu)
	if !ok || f == nil {
		t.Fatal("lookup wuu failed after registration")
	}
	// Legacy empty ids resolve to the built-in engine.
	if _, ok := reg.Lookup(""); !ok {
		t.Fatal("lookup of empty id should normalize to wuu")
	}
	if _, ok := reg.Lookup(EngineID("claude")); ok {
		t.Fatal("claude must not resolve before registration")
	}
	descs := reg.Descriptors()
	if len(descs) != 1 || descs[0].ID != EngineWuu || descs[0].Version != "1" {
		t.Fatalf("Descriptors() = %+v, want [wuu v1]", descs)
	}
}

func TestRegistryRegisterRequiresFactory(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(nil); err == nil {
		t.Fatal("Register(nil) must fail")
	}
	if err := reg.Register(fakeFactory{desc: Descriptor{ID: "", Version: "1"}}); err == nil {
		t.Fatal("Register with empty descriptor id must fail")
	}
}
