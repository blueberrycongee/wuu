package personalhost

// OperationAccess enumerates the supported host boundary. Execution operations
// require a separately verified session; all other operations additionally need
// a live owner admission. Read describes effect scope, not permission exemption.
func OperationAccess(operation string) (read, execution, known bool) {
	switch operation {
	case "memory.read", "memory.operation", "memory.source", "review.execution.status":
		return true, true, true
	case "memory.commit":
		return false, true, true
	case "memory.operations", "owner.check", "review.list":
		return true, false, true
	case "memory.reconcile", "review.settings", "review.resolve", "review.revoke":
		return false, false, true
	default:
		return false, false, false
	}
}
