package automation

import "github.com/blueberrycongee/wuu/internal/schedule"

type CronExpression = schedule.CronExpression

var ParseCronExpression = schedule.ParseCronExpression
var IntervalToCron = schedule.IntervalToCron
