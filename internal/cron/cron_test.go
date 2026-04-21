package cron

import (
	"testing"
	"time"
)

func TestParseCronExpression_valid(t *testing.T) {
	cases := []struct {
		input string
		want  CronExpression
	}{
		{"*/5 * * * *", CronExpression{Minute: "*/5", Hour: "*", DayOfMonth: "*", Month: "*", DayOfWeek: "*"}},
		{"0 9 * * 1-5", CronExpression{Minute: "0", Hour: "9", DayOfMonth: "*", Month: "*", DayOfWeek: "1-5"}},
	}
	for _, c := range cases {
		got, err := ParseCronExpression(c.input)
		if err != nil {
			t.Fatalf("ParseCronExpression(%q) error: %v", c.input, err)
		}
		if got != c.want {
			t.Fatalf("ParseCronExpression(%q) = %+v, want %+v", c.input, got, c.want)
		}
	}
}

func TestParseCronExpression_invalid(t *testing.T) {
	cases := []string{"", "* * *", "a b c d e", "60 * * * *"}
	for _, input := range cases {
		_, err := ParseCronExpression(input)
		if err == nil {
			t.Fatalf("ParseCronExpression(%q) should error", input)
		}
	}
}

func TestNextRun_simple(t *testing.T) {
	cron, _ := ParseCronExpression("*/5 * * * *")
	anchor := time.Date(2024, 1, 1, 12, 0, 0, 0, time.Local)
	next, err := cron.NextRun(anchor)
	if err != nil {
		t.Fatalf("NextRun error: %v", err)
	}
	want := time.Date(2024, 1, 1, 12, 5, 0, 0, time.Local)
	if !next.Equal(want) {
		t.Fatalf("NextRun = %v, want %v", next, want)
	}
}

func TestIntervalToCron(t *testing.T) {
	cases := []struct {
		interval string
		want     string
		wantErr  bool
	}{
		{"5m", "*/5 * * * *", false},
		{"30m", "*/30 * * * *", false},
		{"2h", "0 */2 * * *", false},
		{"1d", "0 0 */1 * *", false},
		{"10s", "*/1 * * * *", false},
		{"", "", true},
		{"5x", "", true},
	}
	for _, c := range cases {
		got, err := IntervalToCron(c.interval)
		if c.wantErr {
			if err == nil {
				t.Fatalf("IntervalToCron(%q) should error", c.interval)
			}
			continue
		}
		if err != nil {
			t.Fatalf("IntervalToCron(%q) error: %v", c.interval, err)
		}
		if got != c.want {
			t.Fatalf("IntervalToCron(%q) = %q, want %q", c.interval, got, c.want)
		}
	}
}
