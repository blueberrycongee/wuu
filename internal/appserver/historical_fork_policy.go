package appserver

import (
	"encoding/json"
	"errors"
	"slices"
)

// Configuration is historical, authority is current. A fork cannot undo a
// current deny or widen the current allowlist merely by selecting an old entry.
func intersectHistoricalToolPolicy(historical, current string, override *hostSessionToolPolicy) (*hostSessionToolPolicy, error) {
	var policies []*hostSessionToolPolicy
	for _, raw := range []string{historical, current} {
		if raw == "" {
			continue
		}
		var p hostSessionToolPolicy
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		policies = append(policies, &p)
	}
	if override != nil {
		policies = append(policies, override)
	}
	out := &hostSessionToolPolicy{}
	for _, p := range policies {
		for _, name := range p.Deny {
			if !slices.Contains(out.Deny, name) {
				out.Deny = append(out.Deny, name)
			}
		}
		if len(p.Allow) == 0 {
			continue
		}
		if len(out.Allow) == 0 {
			out.Allow = slices.Clone(p.Allow)
			continue
		}
		next := make([]string, 0)
		for _, name := range out.Allow {
			if slices.Contains(p.Allow, name) {
				next = append(next, name)
			}
		}
		if len(next) == 0 {
			return nil, errors.New("historical tool selection has no currently allowed tools")
		}
		out.Allow = next
	}
	return out, nil
}
