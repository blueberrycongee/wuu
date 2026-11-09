package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// Test-contract guard: existing tests encode behavioral contracts. Under
// pressure ("this test blocks my fix"), models tend to delete or rename the
// blocking test and invert its assertions instead of scoping the fix. This
// guard does not block the mutation — users legitimately ask for test
// refactors — it makes the removal explicit in the tool result so the model
// must consciously own it rather than slip it into a larger diff.

var testDeclPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^func (Test\w+|Benchmark\w+|Fuzz\w+)\(`),
	regexp.MustCompile(`(?m)\b(?:test|it|describe)\(\s*["'` + "`" + `]([^"'` + "`" + `]+)`),
	regexp.MustCompile(`(?m)^\s*def (test_\w+)\(`),
}

var testFileSuffixes = []string{
	"_test.go",
	".test.ts", ".test.tsx", ".test.js", ".test.jsx", ".test.mjs",
	".spec.ts", ".spec.tsx", ".spec.js", ".spec.jsx",
	"_test.py",
}

func isTestFilePath(path string) bool {
	lower := strings.ToLower(path)
	for _, suffix := range testFileSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func testDeclNames(content string) map[string]struct{} {
	names := make(map[string]struct{})
	for _, pattern := range testDeclPatterns {
		for _, match := range pattern.FindAllStringSubmatch(content, -1) {
			if len(match) > 1 && match[1] != "" {
				names[match[1]] = struct{}{}
			}
		}
	}
	return names
}

// removedTestDecls returns test declarations present in oldContent but absent
// from newContent, in stable order of first appearance in oldContent.
func removedTestDecls(oldContent, newContent string) []string {
	oldNames := testDeclNames(oldContent)
	if len(oldNames) == 0 {
		return nil
	}
	newNames := testDeclNames(newContent)
	var removed []string
	seen := make(map[string]struct{})
	for _, pattern := range testDeclPatterns {
		for _, match := range pattern.FindAllStringSubmatch(oldContent, -1) {
			if len(match) < 2 || match[1] == "" {
				continue
			}
			name := match[1]
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			if _, kept := newNames[name]; !kept {
				removed = append(removed, name)
			}
		}
	}
	return removed
}

// testContractWarning builds the model-facing warning for removed tests, or
// "" when the mutation removes none.
func testContractWarning(path, oldContent, newContent string) string {
	if !isTestFilePath(path) {
		return ""
	}
	removed := removedTestDecls(oldContent, newContent)
	if len(removed) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"this edit removes existing test(s): %s. Existing tests encode behavioral contracts; do not delete, rename, or invert a test because it blocks your implementation — scope the implementation to respect it instead. If the test appears to contradict the requested fix, first check whether it covers a different scenario than the request (it usually does — e.g. a different trigger, state, or error class) and make both hold. Keep this removal only if the user explicitly asked for it or the test provably describes the exact scenario the user wants changed, and say so in your final answer.",
		strings.Join(removed, ", "),
	)
}
