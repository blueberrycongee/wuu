package agentcontrol

const requiresReportWorkerType = "requires_report_test"

func init() {
	builtinWorkerTypes[requiresReportWorkerType] = WorkerType{
		Name:           requiresReportWorkerType,
		Role:           "Requires Report Test",
		RequiresReport: true,
		Internal:       true,
	}
}
