// Package sidethread persists the read-only companion conversation opened by
// /side. Each main thread owns at most one side thread, stored separately from
// regular sessions and omitted from the global session list.
package sidethread
