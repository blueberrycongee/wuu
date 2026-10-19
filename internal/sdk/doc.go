// Package sdk is Wuu's internal application-embedding facade for the
// UI-neutral agent host. It is not a public Go API; it exists to let the
// wuu exec and wuu app-server entrypoints share one in-process client over the
// versioned app-server protocol. Plugin authors use packages/plugin-sdk and
// do not need this package.
//
// A Runtime owns the provider clients, durable session mechanisms, tools,
// plugin generations, and recovery services for one workspace. Applications
// can either serve the versioned app-server protocol to another process or use
// Client and Session to drive that same protocol in-process. Neither path
// imports or replaces agent-loop, persistence, permission, or plugin internals.
//
// Client, Session, and Run are lightweight handles over authoritative
// app-server records, not mutable agent-loop objects. Creating, resuming, or
// forking a session returns another handle without replacing the Runtime or
// invalidating connection-scoped subscriptions. Server notifications update the
// snapshots exposed by those handles.
//
// Operation contexts bound local requests and waits; use Run.Cancel to request
// server-side interruption. Client.Close performs protocol shutdown, and
// Runtime.Close releases workspace resources after its connections have ended.
// Goal, Subagent, Automation, Memory, Dream, and Plan remain complete
// plugin-owned product slices composed through versioned plugin contracts;
// embedding the host does not expose their private state or policy. Loop-driver
// registration and selection also remain internal and experimental.
//
// The facade intentionally covers the shared session and run lifecycle. More
// specialized host controls remain on the versioned app-server protocol until
// they have stable, host-neutral SDK semantics.
package sdk
