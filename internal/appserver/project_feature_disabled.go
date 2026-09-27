//go:build !project_agent

package appserver

// Project Agent is opt-in at build time, never through user configuration.
const projectAgentEnabled = false
