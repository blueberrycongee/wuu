package providers

import "time"

// RetryConfigForProfile is the single source of truth for inference recovery
// budgets. Unary and streaming executors use the same attempt ceiling.
//
// Interactive agent work gets a larger recovery window because one logical
// turn can span many provider calls and transient failures share its workflow
// budget. Auxiliary workloads retain the smaller established allowance.
func RetryConfigForProfile(profile InferenceWorkloadProfile) RetryConfig {
	switch profile {
	case InferenceProfileInteractive, InferenceProfileBackgroundAgent:
		return RetryConfig{
			MaxRetries:   10,
			InitialDelay: time.Second,
			MaxDelay:     time.Minute,
		}
	case InferenceProfileContinuationCritical, InferenceProfileBestEffort:
		return RetryConfig{
			MaxRetries:   3,
			InitialDelay: time.Second,
			MaxDelay:     time.Minute,
		}
	default:
		// Unknown workloads fail closed to one provider attempt. New product
		// paths must choose a profile explicitly before gaining replay budget.
		return DefaultRetryConfig()
	}
}

// WorkflowBudgetSpecForProfile centralizes the recovery spend shared across
// all operations in one workflow. Planned agent rounds remain governed by the
// agent loop; this budget specifically prevents retries and recovery children
// from each minting a fresh allowance.
func WorkflowBudgetSpecForProfile(profile InferenceWorkloadProfile) WorkflowBudgetSpec {
	cfg := NormalizeRetryConfig(RetryConfigForProfile(profile))
	replays := uint64(cfg.MaxRetries)
	maxWait := uint64((time.Duration(cfg.MaxRetries) * cfg.MaxDelay) / time.Millisecond)
	credentialRefreshes := uint64(0)
	if cfg.MaxRetries > 0 {
		credentialRefreshes = 1
	}
	return WorkflowBudgetSpec{
		MaxSamePayloadReplays:         LimitedBudget(replays),
		MaxTransportSwitches:          LimitedBudget(replays),
		MaxCredentialRefreshes:        LimitedBudget(credentialRefreshes),
		MaxPayloadTransforms:          LimitedBudget(replays),
		MaxRecoveryWaitMillis:         LimitedBudget(maxWait),
		MaxUnknownBillableSubmissions: LimitedBudget(uint64(cfg.MaxRetries + 1)),
	}
}
