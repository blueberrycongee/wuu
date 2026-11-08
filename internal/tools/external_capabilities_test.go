package tools

import "testing"

// A turn-scoped reference admission must union with the surface-lifetime
// admission, and clearing it must leave the surface admission untouched —
// replacing or wiping the same map loses every enabled-server grant installed
// when the capability runtime was created.
func TestTurnConversationToolsUnionWithSurfaceAdmission(t *testing.T) {
	kit := &Toolkit{env: &Env{}}
	kit.SetConversationTools(nil, []string{"surface_tool"})
	kit.SetTurnConversationTools(nil, []string{"turn_tool"})

	if !kit.externalCapabilityAllowed("surface_tool") {
		t.Fatal("turn admission removed the surface-lifetime tool")
	}
	if !kit.externalCapabilityAllowed("turn_tool") {
		t.Fatal("turn admission did not add the referenced tool")
	}
	if kit.externalCapabilityAllowed("never_admitted") {
		t.Fatal("unlisted tool was admitted")
	}

	kit.SetTurnConversationTools(nil, nil)
	if !kit.externalCapabilityAllowed("surface_tool") {
		t.Fatal("clearing turn admission removed the surface-lifetime tool")
	}
	if kit.externalCapabilityAllowed("turn_tool") {
		t.Fatal("cleared turn admission still admits the referenced tool")
	}
}
