package jsonl

import (
	"strings"
	"testing"
)

func makeInput(lines, lineLen int) string {
	line := strings.Repeat("x", lineLen-1) + "\n"
	return strings.Repeat(line, lines)
}

func benchmarkForEachLine(b *testing.B, fn func(r *strings.Reader) error, input string) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = fn(strings.NewReader(input))
	}
}

func BenchmarkSmallLines_Go(b *testing.B) {
	input := makeInput(100000, 20)
	benchmarkForEachLine(b, func(r *strings.Reader) error {
		var sum int
		return ForEachLine(r, func(line []byte) error {
			sum += len(line)
			return nil
		})
	}, input)
}

func BenchmarkSmallLines_Zig(b *testing.B) {
	input := makeInput(100000, 20)
	benchmarkForEachLine(b, func(r *strings.Reader) error {
		var sum int
		return ForEachLineZig(r, func(line []byte) error {
			sum += len(line)
			return nil
		})
	}, input)
}

func BenchmarkMediumLines_Go(b *testing.B) {
	input := makeInput(10000, 1024)
	benchmarkForEachLine(b, func(r *strings.Reader) error {
		var sum int
		return ForEachLine(r, func(line []byte) error {
			sum += len(line)
			return nil
		})
	}, input)
}

func BenchmarkMediumLines_Zig(b *testing.B) {
	input := makeInput(10000, 1024)
	benchmarkForEachLine(b, func(r *strings.Reader) error {
		var sum int
		return ForEachLineZig(r, func(line []byte) error {
			sum += len(line)
			return nil
		})
	}, input)
}

func BenchmarkLargeLines_Go(b *testing.B) {
	input := makeInput(1000, 100*1024)
	benchmarkForEachLine(b, func(r *strings.Reader) error {
		var sum int
		return ForEachLine(r, func(line []byte) error {
			sum += len(line)
			return nil
		})
	}, input)
}

func BenchmarkLargeLines_Zig(b *testing.B) {
	input := makeInput(1000, 100*1024)
	benchmarkForEachLine(b, func(r *strings.Reader) error {
		var sum int
		return ForEachLineZig(r, func(line []byte) error {
			sum += len(line)
			return nil
		})
	}, input)
}

func BenchmarkHugeLine_Go(b *testing.B) {
	input := strings.Repeat("x", 10*1024*1024) + "\n"
	benchmarkForEachLine(b, func(r *strings.Reader) error {
		var sum int
		return ForEachLine(r, func(line []byte) error {
			sum += len(line)
			return nil
		})
	}, input)
}

func BenchmarkHugeLine_Zig(b *testing.B) {
	input := strings.Repeat("x", 10*1024*1024) + "\n"
	benchmarkForEachLine(b, func(r *strings.Reader) error {
		var sum int
		return ForEachLineZig(r, func(line []byte) error {
			sum += len(line)
			return nil
		})
	}, input)
}

func BenchmarkMixed_Go(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 10000; i++ {
		sb.WriteString(strings.Repeat("x", i%1000+1))
		sb.WriteByte('\n')
	}
	input := sb.String()
	benchmarkForEachLine(b, func(r *strings.Reader) error {
		var sum int
		return ForEachLine(r, func(line []byte) error {
			sum += len(line)
			return nil
		})
	}, input)
}

func BenchmarkMixed_Zig(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 10000; i++ {
		sb.WriteString(strings.Repeat("x", i%1000+1))
		sb.WriteByte('\n')
	}
	input := sb.String()
	benchmarkForEachLine(b, func(r *strings.Reader) error {
		var sum int
		return ForEachLineZig(r, func(line []byte) error {
			sum += len(line)
			return nil
		})
	}, input)
}

func BenchmarkNoTrailingNewline_Go(b *testing.B) {
	input := strings.Repeat("x", 100*1024) // no trailing newline
	benchmarkForEachLine(b, func(r *strings.Reader) error {
		var sum int
		return ForEachLine(r, func(line []byte) error {
			sum += len(line)
			return nil
		})
	}, input)
}

func BenchmarkNoTrailingNewline_Zig(b *testing.B) {
	input := strings.Repeat("x", 100*1024) // no trailing newline
	benchmarkForEachLine(b, func(r *strings.Reader) error {
		var sum int
		return ForEachLineZig(r, func(line []byte) error {
			sum += len(line)
			return nil
		})
	}, input)
}
