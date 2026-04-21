package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type CronExpression struct {
	Minute     string
	Hour       string
	DayOfMonth string
	Month      string
	DayOfWeek  string
}

func ParseCronExpression(input string) (CronExpression, error) {
	fields := strings.Fields(input)
	if len(fields) != 5 {
		return CronExpression{}, fmt.Errorf("cron expression must have exactly 5 fields, got %d", len(fields))
	}
	ce := CronExpression{
		Minute:     fields[0],
		Hour:       fields[1],
		DayOfMonth: fields[2],
		Month:      fields[3],
		DayOfWeek:  fields[4],
	}
	for i, f := range fields {
		if !isValidCronField(f, i) {
			return CronExpression{}, fmt.Errorf("invalid cron field %q at position %d", f, i)
		}
	}
	return ce, nil
}

var fieldBounds = [5]struct{ min, max int }{
	{0, 59},  // minute
	{0, 23},  // hour
	{1, 31},  // day of month
	{1, 12},  // month
	{0, 7},   // day of week (7 = Sunday, mapped to 0)
}

func isValidCronField(field string, position int) bool {
	if field == "*" || field == "?" {
		return true
	}
	bounds := fieldBounds[position]
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return false
		}
		base := part
		if strings.Contains(part, "/") {
			parts := strings.SplitN(part, "/", 2)
			if len(parts) != 2 {
				return false
			}
			stepVal, err := strconv.Atoi(parts[1])
			if err != nil || stepVal <= 0 {
				return false
			}
			base = parts[0]
		}
		if base == "*" {
			continue
		}
		if strings.Contains(base, "-") {
			parts := strings.SplitN(base, "-", 2)
			if len(parts) != 2 {
				return false
			}
			start, err1 := strconv.Atoi(parts[0])
			end, err2 := strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil {
				return false
			}
			if start < bounds.min || end > bounds.max || start > end {
				return false
			}
			continue
		}
		v, err := strconv.Atoi(base)
		if err != nil {
			return false
		}
		if v < bounds.min || v > bounds.max {
			return false
		}
	}
	return true
}

func (ce CronExpression) NextRun(after time.Time) (time.Time, error) {
	start := after.Truncate(time.Minute).Add(time.Minute)
	limit := start.AddDate(4, 0, 0)
	for t := start; t.Before(limit); t = t.Add(time.Minute) {
		if ce.matches(t) {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("no next run found within 4 years")
}

func (ce CronExpression) matches(t time.Time) bool {
	// Standard cron: if neither DayOfMonth nor DayOfWeek is "*" or "?",
	// they are ORed (match either). Otherwise they are ANDed.
	domStar := ce.DayOfMonth == "*" || ce.DayOfMonth == "?"
	dowStar := ce.DayOfWeek == "*" || ce.DayOfWeek == "?"
	dayMatch := matchField(ce.DayOfMonth, t.Day(), 1, 31) &&
		matchField(ce.DayOfWeek, int(t.Weekday()), 0, 6)
	if !domStar && !dowStar {
		dayMatch = matchField(ce.DayOfMonth, t.Day(), 1, 31) ||
			matchField(ce.DayOfWeek, int(t.Weekday()), 0, 6)
	}

	return matchField(ce.Minute, t.Minute(), 0, 59) &&
		matchField(ce.Hour, t.Hour(), 0, 23) &&
		matchField(ce.Month, int(t.Month()), 1, 12) &&
		dayMatch
}

func matchField(field string, value, min, max int) bool {
	if field == "*" || field == "?" {
		return true
	}
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		step := 1
		if strings.Contains(part, "/") {
			parts := strings.SplitN(part, "/", 2)
			s, _ := strconv.Atoi(parts[1])
			if s > 0 {
				step = s
			}
			part = parts[0]
		}
		var start, end int
		if part == "*" {
			start = min
			end = max
		} else if strings.Contains(part, "-") {
			parts := strings.SplitN(part, "-", 2)
			start, _ = strconv.Atoi(parts[0])
			end, _ = strconv.Atoi(parts[1])
		} else {
			start, _ = strconv.Atoi(part)
			end = start
		}
		if value >= start && value <= end && (value-start)%step == 0 {
			return true
		}
	}
	return false
}

func IntervalToCron(interval string) (string, error) {
	interval = strings.TrimSpace(strings.ToLower(interval))
	if interval == "" {
		return "", fmt.Errorf("empty interval")
	}
	numStr := interval
	unit := interval[len(interval)-1:]
	if unit == "m" || unit == "h" || unit == "d" || unit == "s" {
		numStr = interval[:len(interval)-1]
	} else {
		return "", fmt.Errorf("invalid interval unit: %q", unit)
	}
	n, err := strconv.Atoi(numStr)
	if err != nil || n <= 0 {
		return "", fmt.Errorf("invalid interval number: %q", numStr)
	}
	switch unit {
	case "s":
		if n < 60 {
			return "*/1 * * * *", nil
		}
		return IntervalToCron(fmt.Sprintf("%dm", (n+59)/60))
	case "m":
		if n <= 59 {
			return fmt.Sprintf("*/%d * * * *", n), nil
		}
		h := (n + 59) / 60 // Round up, consistent with seconds path.
		return fmt.Sprintf("0 */%d * * *", h), nil
	case "h":
		if n <= 23 {
			return fmt.Sprintf("0 */%d * * *", n), nil
		}
		return fmt.Sprintf("0 0 */%d * *", (n+23)/24), nil
	case "d":
		return fmt.Sprintf("0 0 */%d * *", n), nil
	}
	return "", fmt.Errorf("unsupported interval: %q", interval)
}

func (ce CronExpression) HumanReadable() string {
	return fmt.Sprintf("%s %s %s %s %s", ce.Minute, ce.Hour, ce.DayOfMonth, ce.Month, ce.DayOfWeek)
}
