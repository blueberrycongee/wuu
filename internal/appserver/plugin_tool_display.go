package appserver

import "strings"

// restorePluginToolLabels upgrades only the in-memory presentation of legacy
// calls. Saved labels remain authoritative after a plugin is renamed or removed.
func (s *Server) restorePluginToolLabels(turns []Turn) {
	if s.rt == nil || s.rt.PluginHost == nil {
		return
	}
	for ti := range turns {
		for ii := range turns[ti].Items {
			item := &turns[ti].Items[ii]
			if item.Type != ThreadItemToolCall || (item.Display != nil && strings.TrimSpace(item.Display.Label) != "") {
				continue
			}
			tool, ok := s.rt.PluginHost.Tool(item.Name)
			if !ok || tool.Registration.Display == nil {
				continue
			}
			display := tool.Registration.Display.Clone()
			if item.Display != nil {
				display.Kind = item.Display.Kind
				display.Text = item.Display.Text
				display.Capability = item.Display.Capability
			}
			item.Display = display
		}
	}
}
