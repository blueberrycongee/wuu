package personalhost

// OperationAccess enumerates the supported host boundary. Execution operations
// require a separately verified session; all other operations additionally need
// a live owner admission. Read describes effect scope, not permission exemption.
func OperationAccess(operation string) (read, execution, known bool) {
	switch operation {
	case "memory.read", "memory.operation", "memory.source", "review.execution.status", "calendar.read.status", "calendar.read.events", "calendar.read.authorize":
		return true, true, true
	case "memory.commit":
		return false, true, true
	case "memory.operations", "owner.check", "review.list", "calendar.status", "calendar.calendars", "calendar.attempt":
		return true, false, true
	case "memory.reconcile", "review.settings", "review.resolve", "review.revoke", "calendar.select", "calendar.disconnect", "calendar.cancel", "calendar.start":
		return false, false, true
	default:
		return false, false, false
	}
}
