package appserver

import "errors"

var errRetiredSession = errors.New("Project Agent has been removed; this conversation is read-only")

// Historical conversations stay readable, but their old coordination prompts
// and queued inputs must never start execution after the provider is removed.
func retiredSessionSource(source string) bool {
	return source == "project" || source == "project-session"
}
