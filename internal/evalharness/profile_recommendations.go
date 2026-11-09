package evalharness

import (
	"fmt"
	"strconv"
	"time"
)

func BuildModelProfileRecommendations(summary TraceReplaySummary) []ModelProfileRecommendation {
	profile := summary.ModelProfile
	if profile == nil {
		return nil
	}
	var out []ModelProfileRecommendation
	if !profile.AllowParallelReadOnly {
		if evidence := overlappingReadOnlyEvidence(summary.ToolRecords); len(evidence) > 0 {
			out = append(out, ModelProfileRecommendation{
				ProviderName:     profile.ProviderName,
				Model:            profile.Model,
				Field:            "execution.allow_parallel_read_only",
				CurrentValue:     strconv.FormatBool(profile.AllowParallelReadOnly),
				RecommendedValue: "true",
				Reason:           "eval trace observed overlapping successful read-only tool calls marked concurrency-safe",
				Evidence:         evidence,
			})
		}
	}
	return out
}

func overlappingReadOnlyEvidence(records []ToolObservation) []string {
	var evidence []string
	for i := 0; i < len(records); i++ {
		a := records[i]
		if !successfulConcurrentReadOnly(a) {
			continue
		}
		aStart, aEnd, ok := toolObservationWindow(a)
		if !ok {
			continue
		}
		for j := i + 1; j < len(records); j++ {
			b := records[j]
			if !successfulConcurrentReadOnly(b) {
				continue
			}
			bStart, bEnd, ok := toolObservationWindow(b)
			if !ok || !windowsOverlap(aStart, aEnd, bStart, bEnd) {
				continue
			}
			evidence = append(evidence, fmt.Sprintf("%s overlaps %s", a.Name, b.Name))
			if len(evidence) >= 3 {
				return evidence
			}
		}
	}
	return evidence
}

func successfulConcurrentReadOnly(record ToolObservation) bool {
	return record.Success && record.ReadOnly && record.ConcurrencySafe && record.Name != ""
}

func toolObservationWindow(record ToolObservation) (time.Time, time.Time, bool) {
	if record.StartedAt.IsZero() || record.DurationMS <= 0 {
		return time.Time{}, time.Time{}, false
	}
	start := record.StartedAt
	end := start.Add(time.Duration(record.DurationMS) * time.Millisecond)
	return start, end, true
}

func windowsOverlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}
