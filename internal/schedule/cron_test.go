package schedule

import (
	"testing"
	"time"
)

func TestNextRunWeekdayOccurrences(t *testing.T) {
	// Start after Saturday's run and cross two Sundays, checking that aliases
	// neither duplicate Sunday nor lose the other days selected by a range.
	after := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		dow  string
		days []int
	}{
		{"0", []int{20, 27, 34}},
		{"7", []int{20, 27, 34}},
		{"7-7", []int{20, 27, 34}},
		{"7/2", []int{20, 27, 34}},
		{"6-7", []int{20, 26, 27, 33, 34}},
		{"6,7", []int{20, 26, 27, 33, 34}},
		{"0,6", []int{20, 26, 27, 33, 34}},
		{"5-7/2", []int{20, 25, 27, 32, 34}},
		{"1-7/2", []int{20, 21, 23, 25, 27, 28, 30}},
		{"3-7/4", []int{20, 23, 27, 30}},
		{"6-7/2", []int{26, 33, 40}},
		{"5-7/3", []int{25, 32, 39}},
		{"0,7", []int{20, 27, 34}},
		{"0-7", []int{20, 21, 22, 23, 24, 25, 26, 27, 28}},
		{"0-7/2", []int{20, 22, 24, 26, 27, 29}},
		{"*", []int{20, 21, 22, 23, 24, 25, 26, 27, 28}},
		{"?", []int{20, 21, 22, 23, 24, 25, 26, 27, 28}},
		{"*/2", []int{20, 22, 24, 26, 27, 29}},
		{"*/7", []int{20, 27, 34}},
	} {
		t.Run(tc.dow, func(t *testing.T) {
			expr, err := ParseCronExpression("0 9 * * " + tc.dow)
			if err != nil {
				t.Fatal(err)
			}
			anchor := after
			for i, day := range tc.days {
				want := time.Date(2026, 9, day, 9, 0, 0, 0, time.UTC)
				got, err := expr.NextRun(anchor)
				if err != nil || !got.Equal(want) {
					t.Fatalf("occurrence %d after %s = %s, %v; want %s", i, anchor, got, err, want)
				}
				anchor = got
			}
		})
	}
}

func TestNextRunSundayTimeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, zone, after, want string
	}{
		{"before-minute", "UTC", "2026-09-20T08:59:59Z", "2026-09-20T09:00:00Z"},
		{"at-minute", "UTC", "2026-09-20T09:00:00Z", "2026-09-27T09:00:00Z"},
		{"within-minute", "UTC", "2026-09-20T09:00:00.001Z", "2026-09-27T09:00:00Z"},
		{"local-sunday", "Asia/Shanghai", "2026-09-19T23:59:59Z", "2026-09-20T01:00:00Z"},
		{"spring-DST", "America/New_York", "2026-03-07T12:00:00Z", "2026-03-08T13:00:00Z"},
		{"autumn-DST", "America/New_York", "2026-10-31T12:00:00Z", "2026-11-01T14:00:00Z"},
	} {
		for _, dow := range []string{"0", "7"} {
			t.Run(tc.name+"/"+dow, func(t *testing.T) {
				loc, err := time.LoadLocation(tc.zone)
				if err != nil {
					t.Fatal(err)
				}
				after, err := time.Parse(time.RFC3339Nano, tc.after)
				if err != nil {
					t.Fatal(err)
				}
				expr, err := ParseCronExpression("0 9 * * " + dow)
				if err != nil {
					t.Fatal(err)
				}
				got, err := expr.NextRun(after.In(loc))
				if err != nil || got.UTC().Format(time.RFC3339) != tc.want {
					t.Fatalf("NextRun = %s, %v; want %s", got, err, tc.want)
				}
			})
		}
	}
}

func TestNextRunSundayDayOfMonthUnion(t *testing.T) {
	for _, dow := range []string{"0", "7"} {
		t.Run(dow, func(t *testing.T) {
			expr, err := ParseCronExpression("0 9 21 * " + dow)
			if err != nil {
				t.Fatal(err)
			}
			anchor := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
			for _, day := range []int{20, 21, 27} {
				want := time.Date(2026, 9, day, 9, 0, 0, 0, time.UTC)
				got, err := expr.NextRun(anchor)
				if err != nil || !got.Equal(want) {
					t.Fatalf("NextRun after %s = %s, %v; want %s", anchor, got, err, want)
				}
				anchor = got
			}
		})
	}
}

func TestParseCronRejectsInvalidWeekdays(t *testing.T) {
	for _, dow := range []string{"8", "14", "-1", "6-8", "7-0", "7/0", "5-7/0", "0,8", "7,", "5-7/-2"} {
		t.Run(dow, func(t *testing.T) {
			if _, err := ParseCronExpression("0 9 * * " + dow); err == nil {
				t.Fatal("invalid weekday accepted")
			}
		})
	}
}
