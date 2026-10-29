package personalhost

// OperationAccess enumerates the supported host boundary. Execution operations
// require a separately verified session; all other operations additionally need
// a live owner admission. Read describes effect scope, not permission exemption.
func OperationAccess(operation string) (read, execution, known bool) {
	switch operation {
	case "memory.read", "memory.operation", "memory.source", "review.execution.status", "calendar.read.status", "calendar.read.events", "calendar.read.authorize", "connectors.read.list", "connectors.read.actions":
		return true, true, true
	case "memory.commit", "connectors.call", "connectors.poll":
		return false, true, true
	case "memory.operations", "owner.check", "review.list", "calendar.status", "calendar.calendars", "calendar.attempt", "connectors.list", "connectors.actions", "connectors.attempt":
		return true, false, true
	case "memory.reconcile", "review.settings", "review.resolve", "review.revoke", "calendar.select", "calendar.disconnect", "calendar.cancel", "calendar.start", "connectors.start", "connectors.cancel", "connectors.decide", "connectors.disconnect":
		return false, false, true
	default:
		return false, false, false
	}
}
