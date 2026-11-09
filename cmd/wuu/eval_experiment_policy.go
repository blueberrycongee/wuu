package main

import (
	"errors"
	"fmt"
	"math"
)

// Policy is frozen with the task denominator, not selected by the candidate.
// Durations are wall-clock pair durations with identical admission concurrency.
type evalExperimentPolicy struct {
	Concurrency int                  `json:"concurrency"`
	Acceptance  evalAcceptancePolicy `json:"acceptance"`
}

type evalAcceptancePolicy struct {
	RequireAllPass   bool    `json:"require_all_pass"`
	Improvement      string  `json:"improvement"`
	MaxDurationRatio float64 `json:"max_duration_ratio"`
}

func defaultEvalExperimentPolicy() evalExperimentPolicy {
	return evalExperimentPolicy{Concurrency: 1, Acceptance: evalAcceptancePolicy{RequireAllPass: true, Improvement: "turns-or-tokens", MaxDurationRatio: 1.25}}
}

func (p evalExperimentPolicy) validate() error {
	if p.Concurrency < 1 || p.Concurrency > 256 {
		return errors.New("experiment concurrency must be between 1 and 256")
	}
	if math.IsNaN(p.Acceptance.MaxDurationRatio) || math.IsInf(p.Acceptance.MaxDurationRatio, 0) || p.Acceptance.MaxDurationRatio <= 0 {
		return errors.New("acceptance max_duration_ratio must be positive and finite")
	}
	switch p.Acceptance.Improvement {
	case "turns-or-tokens", "turns", "tokens", "quality", "non-regression":
	default:
		return fmt.Errorf("unknown acceptance improvement %q", p.Acceptance.Improvement)
	}
	return nil
}

func (p evalAcceptancePolicy) accepts(reports [2]evalReport, turns, tokens [2]int) bool {
	bs, cs := summarizeEvalResults(reports[0].Results), summarizeEvalResults(reports[1].Results)
	if p.RequireAllPass && (bs.Failed != 0 || cs.Failed != 0) {
		return false
	}
	if cs.Passed < bs.Passed || float64(reports[1].DurationMS) > p.MaxDurationRatio*float64(reports[0].DurationMS) {
		return false
	}
	switch p.Improvement {
	case "turns-or-tokens":
		return turns[1] < turns[0] || tokens[1] < tokens[0]
	case "turns":
		return turns[1] < turns[0]
	case "tokens":
		return tokens[1] < tokens[0]
	case "quality":
		return cs.Passed > bs.Passed
	case "non-regression":
		return turns[1] <= turns[0] && tokens[1] <= tokens[0]
	}
	return false
}
