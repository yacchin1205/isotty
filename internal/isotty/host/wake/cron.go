package wake

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a schedule in the five fields of cron, minute, hour, day of
// month, month and day of week, in the host's local time: the human's
// clock, and what Claude Code's own CronCreate takes. Each field is `*`, a
// number, a range `a-b`, any of them with a step `/n`, or a list of those.
// As in cron, when both days are restricted either one matching is enough.
type Cron struct {
	minute, hour, day, month, weekday []bool
	dayAny, weekdayAny                bool
}

func ParseCron(spec string) (Cron, error) {
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return Cron{}, fmt.Errorf("cron %q needs five fields: minute hour day-of-month month day-of-week", spec)
	}
	var c Cron
	var err error
	if c.minute, _, err = cronField(fields[0], 0, 59); err != nil {
		return Cron{}, fmt.Errorf("cron minute: %w", err)
	}
	if c.hour, _, err = cronField(fields[1], 0, 23); err != nil {
		return Cron{}, fmt.Errorf("cron hour: %w", err)
	}
	if c.day, c.dayAny, err = cronField(fields[2], 1, 31); err != nil {
		return Cron{}, fmt.Errorf("cron day of month: %w", err)
	}
	if c.month, _, err = cronField(fields[3], 1, 12); err != nil {
		return Cron{}, fmt.Errorf("cron month: %w", err)
	}
	if c.weekday, c.weekdayAny, err = cronField(fields[4], 0, 7); err != nil {
		return Cron{}, fmt.Errorf("cron day of week: %w", err)
	}
	// 7 is Sunday too.
	c.weekday[0] = c.weekday[0] || c.weekday[7]
	return c, nil
}

func cronField(field string, low, high int) ([]bool, bool, error) {
	set := make([]bool, high+1)
	for _, part := range strings.Split(field, ",") {
		rangePart, step := part, 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n < 1 {
				return nil, false, fmt.Errorf("bad step in %q", part)
			}
			rangePart, step = part[:i], n
		}
		from, to := low, high
		if rangePart != "*" {
			bounds := strings.SplitN(rangePart, "-", 2)
			var err error
			if from, err = strconv.Atoi(bounds[0]); err != nil {
				return nil, false, fmt.Errorf("bad value %q", part)
			}
			to = from
			if len(bounds) == 2 {
				if to, err = strconv.Atoi(bounds[1]); err != nil {
					return nil, false, fmt.Errorf("bad value %q", part)
				}
			} else if step > 1 {
				to = high
			}
		}
		if from < low || to > high || from > to {
			return nil, false, fmt.Errorf("%q is outside %d-%d", part, low, high)
		}
		for v := from; v <= to; v += step {
			set[v] = true
		}
	}
	return set, field == "*", nil
}

// Next is the first minute after t that the schedule names.
func (c Cron) Next(t time.Time) time.Time {
	t = t.Truncate(time.Minute).Add(time.Minute)
	// Every schedule names a minute within some years: 29 February on a
	// Monday comes round within 28.
	for end := t.AddDate(28, 0, 0); t.Before(end); {
		if !c.month[int(t.Month())] {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.hour[t.Hour()] {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if !c.minute[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

func (c Cron) dayMatches(t time.Time) bool {
	day, weekday := c.day[t.Day()], c.weekday[int(t.Weekday())]
	switch {
	case c.dayAny && c.weekdayAny:
		return true
	case c.dayAny:
		return weekday
	case c.weekdayAny:
		return day
	}
	return day || weekday
}
