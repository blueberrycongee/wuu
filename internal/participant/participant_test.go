package participant

import "testing"

func TestDeriveEphemeralName(t *testing.T) {
	cases := []struct{ taskName, typ, want string }{
		{"auth_flow_review", "reviewer", "Reviewer·auth_flow_review"},
		{"", "researcher", "Researcher"},
		{"fix_races", "", "Agent·fix_races"},
	}
	for _, c := range cases {
		if got := DeriveEphemeralName(c.taskName, c.typ); got != c.want {
			t.Errorf("DeriveEphemeralName(%q,%q) = %q, want %q", c.taskName, c.typ, got, c.want)
		}
	}
}
