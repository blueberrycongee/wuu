package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Per-tool projectors. Each understands its tool's JSON envelope and reduces it
// to fit the token budget by dropping WHOLE records or WHOLE lines — never by
// slicing the serialized JSON, which would corrupt structure. Every projector
// preserves the envelope's scalar evidence (counts, revision, flags) and adds a
// "projection" object pointing at the recoverable artifact.
//
// Projectors are deterministic and fail open (return ok=false) on any malformed
// payload so the finalizer keeps the full result.

func init() {
	toolProjectors["glob"] = projectGlobResult
	toolProjectors["list_files"] = projectListFilesResult
	toolProjectors["grep"] = projectGrepResult
	toolProjectors["read_file"] = projectReadFileResult
	toolProjectors["bash"] = projectBashResult
	toolProjectors["thread_get"] = projectThreadGetResult

	// bash embeds a recoverable full log in its own envelope; reuse it instead
	// of persisting a duplicate copy of the raw result.
	projectionArtifactExtractors["bash"] = extractBashFullLogRef
}

// parseToolEnvelope decodes a tool's JSON envelope while preserving numbers
// exactly (json.Number) so re-serialization is faithful and deterministic.
func parseToolEnvelope(rawText string) (map[string]any, bool) {
	dec := json.NewDecoder(strings.NewReader(rawText))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

// marshalEnvelope serializes a projected envelope. Map keys are sorted by
// encoding/json, so identical input yields identical bytes.
func marshalEnvelope(m map[string]any) (string, bool) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return "", false
	}
	return strings.TrimRight(buf.String(), "\n"), true
}

// largestFitting returns the greatest keep in [0,total] whose candidate size is
// within budget. size(keep) must be non-decreasing in keep.
func largestFitting(total, budget int, size func(keep int) int) int {
	lo, hi, best := 0, total, 0
	for lo <= hi {
		mid := (lo + hi) / 2
		if size(mid) <= budget {
			best = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return best
}

// setProjectionMeta records that the envelope was projected, points the model
// at the recoverable artifact, and marks the result truncated. omitted carries
// tool-specific omission counts already surfaced elsewhere in the envelope.
func setProjectionMeta(m map[string]any, budget int, artifactRef, recover string, omitted map[string]any) {
	proj := map[string]any{
		"projected":     true,
		"budget_tokens": budget,
		"artifact_ref":  artifactRef,
		"recover":       recover,
	}
	for k, v := range omitted {
		proj[k] = v
	}
	m["projection"] = proj
	m["truncated"] = true
}

// projectRecordArray is the shared reducer for envelopes whose bulk is a single
// list of records (glob "files", list_files "entries", grep "matches"/"files"/
// "counts"). It keeps the largest prefix of records that fits the budget and
// records how many were dropped.
func projectRecordArray(rawText, arrayKey, recover string, pc projectorContext, extraOmitted func(kept, omitted int) map[string]any) (string, projectionOmission, bool) {
	m, ok := parseToolEnvelope(rawText)
	if !ok {
		return "", projectionOmission{}, false
	}
	arr, ok := m[arrayKey].([]any)
	if !ok {
		return "", projectionOmission{}, false
	}
	total := len(arr)
	offset := intJSONNumber(m["offset"])
	revision, _ := m["workspace_revision"].(string)
	sourceHasMore, _ := m["has_more"].(bool)
	continuationSupported := true
	if value, exists := m["continuation_supported"].(bool); exists {
		continuationSupported = value
	}
	if !continuationSupported {
		recover = fmt.Sprintf("read the full bounded result at %s; narrow the search to inspect matches beyond the execution limit", pc.ArtifactRef)
	}
	setPage := func(candidate map[string]any, kept int) {
		hasMore := sourceHasMore || kept < total
		candidate["offset"] = offset
		candidate["returned_count"] = kept
		if arrayKey == "matches" {
			candidate["returned_match_count"] = kept
			candidate["omitted_match_count"] = intJSONNumber(m["omitted_match_count"]) + total - kept
		}
		candidate["has_more"] = hasMore
		if continuationSupported {
			candidate["page"] = continuationPage(offset, kept, hasMore, revision)
		} else {
			candidate["page"] = boundedSearchPage(kept, hasMore)
		}
	}

	size := func(keep int) int {
		candidate := cloneShallow(m)
		candidate[arrayKey] = arr[:keep]
		setPage(candidate, keep)
		om := map[string]any{"omitted_" + singular(arrayKey): total - keep}
		if extraOmitted != nil {
			for k, v := range extraOmitted(keep, total-keep) {
				om[k] = v
			}
		}
		setProjectionMeta(candidate, pc.BudgetTokens, pc.ArtifactRef, recover, om)
		s, ok := marshalEnvelope(candidate)
		if !ok {
			return pc.BudgetTokens + 1
		}
		return estimateResultTokens(s)
	}

	keep := largestFitting(total, pc.BudgetTokens, size)
	if total > 0 && keep == 0 {
		return "", projectionOmission{}, false
	}
	omitted := total - keep
	m[arrayKey] = arr[:keep]
	setPage(m, keep)
	om := map[string]any{"omitted_" + singular(arrayKey): omitted}
	if extraOmitted != nil {
		for k, v := range extraOmitted(keep, omitted) {
			om[k] = v
		}
	}
	setProjectionMeta(m, pc.BudgetTokens, pc.ArtifactRef, recover, om)
	out, ok := marshalEnvelope(m)
	if !ok {
		return "", projectionOmission{}, false
	}
	return out, projectionOmission{Records: omitted}, true
}

func intJSONNumber(value any) int {
	switch typed := value.(type) {
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}

func cloneShallow(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// singular maps a plural array key to a compact singular label for the
// omitted-count field ("files" -> "file", "entries" -> "entry").
func singular(arrayKey string) string {
	switch arrayKey {
	case "entries":
		return "entry"
	case "matches":
		return "match"
	case "files":
		return "file"
	case "counts":
		return "count"
	default:
		return arrayKey
	}
}

func projectGlobResult(rawText string, pc projectorContext) (string, projectionOmission, bool) {
	return projectRecordArray(rawText, "files",
		fmt.Sprintf("use page.next with the same glob arguments for the next non-overlapping page; the full match list is saved at %s", pc.ArtifactRef),
		pc, nil)
}

func projectListFilesResult(rawText string, pc projectorContext) (string, projectionOmission, bool) {
	return projectRecordArray(rawText, "entries",
		fmt.Sprintf("use page.next with the same list_files path for the next non-overlapping page; the full listing is saved at %s", pc.ArtifactRef),
		pc, nil)
}

// projectGrepResult handles all three grep output modes by reducing whichever
// record array the envelope carries: content ("matches"), files_with_matches
// ("files"), or count ("counts").
func projectGrepResult(rawText string, pc projectorContext) (string, projectionOmission, bool) {
	m, ok := parseToolEnvelope(rawText)
	if !ok {
		return "", projectionOmission{}, false
	}
	arrayKey := ""
	for _, key := range []string{"matches", "files", "counts"} {
		if _, ok := m[key].([]any); ok {
			arrayKey = key
			break
		}
	}
	if arrayKey == "" {
		return "", projectionOmission{}, false
	}
	recover := fmt.Sprintf("use page.next with the same grep arguments for the next non-overlapping page; the full matches are saved at %s", pc.ArtifactRef)
	// grep content mode also carries returned/omitted match counters that the
	// model relies on; keep them consistent with what actually remains.
	extra := func(kept, omitted int) map[string]any {
		if arrayKey != "matches" {
			return nil
		}
		return map[string]any{
			"returned_match_count": kept,
			"omitted_match_count":  omitted,
		}
	}
	out, om, ok := projectRecordArray(rawText, arrayKey, recover, pc, extra)
	if !ok {
		return "", projectionOmission{}, false
	}
	return out, om, true
}

// contentLines splits a read_file line-numbered content blob into whole lines,
// dropping the trailing empty element produced by the terminating newline.
func contentLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// projectReadFileResult keeps a continuous prefix of whole lines. Continuation
// covers only the remainder of the requested range, without preview text in content.
func projectReadFileResult(rawText string, pc projectorContext) (string, projectionOmission, bool) {
	m, ok := parseToolEnvelope(rawText)
	if !ok {
		return "", projectionOmission{}, false
	}
	// Byte recovery pages already have a bounded payload and a cursor covering
	// the remaining artifact segment. Reinterpreting them as numbered lines can
	// replace that cursor or falsely declare the segment complete.
	if m["action"] != "read" {
		return "", projectionOmission{}, false
	}
	content, ok := m["content"].(string)
	if !ok {
		return "", projectionOmission{}, false
	}
	lines := contentLines(content)
	numLines := len(lines)
	path, _ := m["path"].(string)
	recover := fmt.Sprintf("use continuation.next to read the remaining requested lines without overlap, or open the full saved result at %s", pc.ArtifactRef)
	startLine := intJSONNumber(m["start_line"])
	if startLine <= 0 {
		startLine = 1
	}
	contentSHA, _ := m["content_sha256"].(string)
	setContinuation := func(candidate map[string]any, keep, omitted int) {
		continuation := map[string]any{"has_more": omitted > 0}
		if omitted > 0 {
			continuation["next"] = map[string]any{
				"continuation": encodeReadFileContinuation(path, startLine+keep, omitted, contentSHA),
			}
		}
		candidate["continuation"] = continuation
		candidate["num_lines"] = keep
		candidate["range"] = readFileRangeMetadata(startLine, keep)
		candidate["omitted_ranges"] = readFileOmittedRanges(intJSONNumber(m["total_lines"]), startLine, keep)
	}

	build := func(keep int) (string, int) {
		if keep >= numLines {
			return content, 0
		}
		return strings.Join(lines[:keep], "\n") + "\n", numLines - keep
	}

	size := func(keep int) int {
		c, omitted := build(keep)
		cand := cloneShallow(m)
		cand["content"] = c
		setContinuation(cand, keep, omitted)
		setProjectionMeta(cand, pc.BudgetTokens, pc.ArtifactRef, recover, map[string]any{
			"omitted_lines": omitted,
			"shown_lines":   keep,
		})
		s, ok := marshalEnvelope(cand)
		if !ok {
			return pc.BudgetTokens + 1
		}
		return estimateResultTokens(s)
	}

	keep := largestFitting(numLines, pc.BudgetTokens, size)
	// An oversized single line must not produce a non-advancing cursor.
	if numLines > 0 && keep == 0 {
		return "", projectionOmission{}, false
	}
	c, omitted := build(keep)
	m["content"] = c
	setContinuation(m, keep, omitted)
	setProjectionMeta(m, pc.BudgetTokens, pc.ArtifactRef, recover, map[string]any{
		"omitted_lines": omitted,
		"shown_lines":   keep,
	})
	out, ok := marshalEnvelope(m)
	if !ok {
		return "", projectionOmission{}, false
	}
	return out, projectionOmission{Lines: omitted}, true
}

func extractBashFullLogRef(rawText string) string {
	m, ok := parseToolEnvelope(rawText)
	if !ok {
		return ""
	}
	ref, _ := m["full_log_ref"].(string)
	return ref
}

// lastLines returns the last n whole lines of s.
func lastLines(s string, n int) string {
	if n <= 0 || s == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if n >= len(lines) {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

func lineCount(s string) int {
	if strings.TrimRight(s, "\n") == "" {
		return 0
	}
	return strings.Count(strings.TrimRight(s, "\n"), "\n") + 1
}

// projectBashResult prioritizes failure evidence. It drops the redundant
// combined "output" field (recoverable via full_log_ref, and duplicated by the
// tails), always keeps exit code / timeout / duration / revision / verification,
// and trims stdout then stderr tails by whole lines — most recent first — to fit
// the budget. Verification evidence is never dropped. If the remainder still
// exceeds the budget, the projector declines so generic settlement can bound it.
func projectBashResult(rawText string, pc projectorContext) (string, projectionOmission, bool) {
	m, ok := parseToolEnvelope(rawText)
	if !ok {
		return "", projectionOmission{}, false
	}
	droppedOutput := 0
	if out, ok := m["output"].(string); ok {
		droppedOutput = len(out)
	}
	delete(m, "output")

	stdout, _ := m["stdout_tail"].(string)
	stderr, _ := m["stderr_tail"].(string)

	build := func(so, se string) (string, int) {
		cand := cloneShallow(m)
		cand["stdout_tail"] = so
		cand["stderr_tail"] = se
		cand["continuation"] = bashProjectionContinuation(m, pc.ArtifactRef, so, se)
		setProjectionMeta(cand, pc.BudgetTokens, pc.ArtifactRef,
			fmt.Sprintf("open the full command log at %s", pc.ArtifactRef),
			map[string]any{
				"dropped_output_bytes": droppedOutput,
				"stdout_tail_trimmed":  lineCount(so) < lineCount(stdout),
				"stderr_tail_trimmed":  lineCount(se) < lineCount(stderr),
			})
		s, ok := marshalEnvelope(cand)
		if !ok {
			return "", pc.BudgetTokens + 1
		}
		return s, estimateResultTokens(s)
	}

	// Fast path: dropping the redundant output may already fit.
	if s, tok := build(stdout, stderr); tok <= pc.BudgetTokens {
		return s, projectionOmission{Bytes: droppedOutput}, true
	}

	// Reduce stdout first (lower priority than stderr).
	soLines := lineCount(stdout)
	keepSo := largestFitting(soLines, pc.BudgetTokens, func(k int) int {
		_, tok := build(lastLines(stdout, k), stderr)
		return tok
	})
	so := lastLines(stdout, keepSo)
	if s, tok := build(so, stderr); tok <= pc.BudgetTokens {
		return s, projectionOmission{Bytes: droppedOutput}, true
	}

	// Still over budget: reduce stderr too (keep the most recent lines).
	seLines := lineCount(stderr)
	keepSe := largestFitting(seLines, pc.BudgetTokens, func(k int) int {
		_, tok := build("", lastLines(stderr, k))
		return tok
	})
	se := lastLines(stderr, keepSe)
	s, tok := build(so, se)
	if tok > pc.BudgetTokens {
		return "", projectionOmission{}, false
	}
	return s, projectionOmission{Bytes: droppedOutput}, true
}

func bashProjectionContinuation(m map[string]any, artifactRef, shownStdout, shownStderr string) map[string]any {
	sections, _ := m["full_log_sections"].(map[string]any)
	fullLogSHA, _ := m["full_log_sha256"].(string)
	type streamRange struct {
		name        string
		start, end  int
		shownSuffix string
	}
	streams := []streamRange{
		{name: "stderr", start: intJSONNumber(sections["stderr_start"]), end: intJSONNumber(sections["stderr_end"]), shownSuffix: shownStderr},
		{name: "stdout", start: intJSONNumber(sections["stdout_start"]), end: intJSONNumber(sections["stdout_end"]), shownSuffix: shownStdout},
	}
	ranges := make([]any, 0, len(streams))
	for _, stream := range streams {
		omittedEnd := stream.end - len(stream.shownSuffix)
		if stream.start < 0 || omittedEnd <= stream.start {
			continue
		}
		ranges = append(ranges, map[string]any{
			"stream": stream.name,
			"next": map[string]any{
				"continuation": encodeReadFileByteContinuation(artifactRef, stream.start, projectionPreviewBytes, omittedEnd, fullLogSHA),
			},
		})
	}
	return map[string]any{
		"kind":     "ranked_artifact_ranges",
		"has_more": len(ranges) > 0,
		"ranges":   ranges,
	}
}
