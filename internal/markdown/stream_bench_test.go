package markdown

import (
	"fmt"
	"strings"
	"testing"
)

// sampleResponseLine gives a realistic mix of markdown tokens.
func sampleResponseLine(i int) string {
	switch i % 7 {
	case 0:
		return fmt.Sprintf("## Section %d\n", i)
	case 1:
		return "- bullet item with **bold** and `code` mixed inline\n"
	case 2:
		return "\n"
	case 3:
		return "Regular prose paragraph, moderate length, nothing unusual here.\n"
	case 4:
		return "```go\nfunc example() { return 42 }\n```\n"
	case 5:
		return fmt.Sprintf("1. Ordered %d with [link](https://example.com/path)\n", i)
	default:
		return "> Blockquoted statement that rephrases the previous paragraph.\n"
	}
}

// BenchmarkStreamCollector_Commit_Cumulative measures the O(N²) cost of
// re-rendering the accumulated buffer on every newline-triggered commit,
// as happens during a real streaming LLM response.
func BenchmarkStreamCollector_Commit_Cumulative(b *testing.B) {
	for _, nLines := range []int{50, 200, 800} {
		b.Run(fmt.Sprintf("lines=%d", nLines), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c := NewStreamCollector(100, DefaultStyles())
				for j := 0; j < nLines; j++ {
					c.Push(sampleResponseLine(j))
					_ = c.Commit()
				}
			}
		})
	}
}

// BenchmarkRender_OneShot is the baseline: what does it cost to render the
// full buffer exactly once (ignoring streaming)?
func BenchmarkRender_OneShot(b *testing.B) {
	for _, nLines := range []int{50, 200, 800} {
		var sb strings.Builder
		for j := 0; j < nLines; j++ {
			sb.WriteString(sampleResponseLine(j))
		}
		src := sb.String()
		b.Run(fmt.Sprintf("lines=%d", nLines), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = Render(src, 100, DefaultStyles())
			}
		})
	}
}

// BenchmarkRender_NoCode skips the code-block lines so we can isolate the
// markdown-without-syntax-highlighting cost and quantify chroma's share.
func BenchmarkRender_NoCode(b *testing.B) {
	for _, nLines := range []int{50, 200, 800} {
		var sb strings.Builder
		for j := 0; j < nLines; j++ {
			if j%7 == 4 {
				sb.WriteString("Regular prose line replacing code sample.\n")
				continue
			}
			sb.WriteString(sampleResponseLine(j))
		}
		src := sb.String()
		b.Run(fmt.Sprintf("lines=%d", nLines), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = Render(src, 100, DefaultStyles())
			}
		})
	}
}
