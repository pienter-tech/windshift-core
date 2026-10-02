package businesstime

import (
	"sort"
	"time"
)

// span is a half-open absolute instant range.
type span struct {
	start time.Time
	end   time.Time
}

func (s span) duration() time.Duration { return s.end.Sub(s.start) }

// OpenIntervals returns the calendar's working intervals that intersect
// [from, to), clipped to those bounds and ordered by start. Holidays and DST
// are handled with the same rules as the other calendar operations.
func (c *Calendar) OpenIntervals(from, to time.Time) []span {
	if !to.After(from) {
		return nil
	}
	var spans []span
	cursor := from
	for cursor.Before(to) {
		local := cursor.In(c.location)
		day := dayAtMidnight(local)
		dayEnd := startOfNextDay(day)
		if !dayEnd.After(cursor) {
			dayEnd = cursor.Add(time.Second)
		}
		limit := dayEnd
		if limit.After(to) {
			limit = to
		}
		if !c.isHoliday(day) {
			for _, interval := range c.weekly[int(local.Weekday())] {
				start, end := c.intervalInstants(day, interval)
				if start.Before(cursor) {
					start = cursor
				}
				if end.After(limit) {
					end = limit
				}
				if end.After(start) {
					spans = append(spans, span{start: start, end: end})
				}
			}
		}
		cursor = limit
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })
	return spans
}

// ElapsedExclusive returns the time in base that is not also in exclude,
// clipped to [from, to). Both calendars keep their own schedule semantics,
// including holidays and DST.
func ElapsedExclusive(base, exclude *Calendar, from, to time.Time) time.Duration {
	return ElapsedExclusiveUnion(base, []*Calendar{exclude}, from, to)
}

// ElapsedExclusiveUnion returns the time in base not covered by any of the
// excludes, clipped to [from, to). It is the multi-reference form of
// ElapsedExclusive and implements the union semantics of "any bound team is
// staffed".
func ElapsedExclusiveUnion(base *Calendar, excludes []*Calendar, from, to time.Time) time.Duration {
	baseSpans := base.OpenIntervals(from, to)
	if len(baseSpans) == 0 {
		return 0
	}
	if len(excludes) == 0 {
		return sumSpans(baseSpans)
	}
	var excluded []span
	for _, calendar := range excludes {
		excluded = append(excluded, calendar.OpenIntervals(from, to)...)
	}
	excluded = mergeSpans(excluded)
	var total time.Duration
	for _, baseSpan := range baseSpans {
		total += baseSpan.duration() - overlapDuration(baseSpan, excluded)
	}
	return total
}

// ElapsedUnion returns the time covered by any of the calendars in
// [from, to). It is the union counterpart of ElapsedExclusiveUnion.
func ElapsedUnion(calendars []*Calendar, from, to time.Time) time.Duration {
	if len(calendars) == 0 {
		return 0
	}
	var spans []span
	for _, calendar := range calendars {
		spans = append(spans, calendar.OpenIntervals(from, to)...)
	}
	return sumSpans(mergeSpans(spans))
}

// ReferencesWithinCalendarHours reports whether any reference calendar is open
// at instant. It is the union-equivalent of WithinCalendarHours.
func ReferencesWithinCalendarHours(references []*Calendar, instant time.Time) bool {
	for _, calendar := range references {
		if calendar.WithinCalendarHours(instant) {
			return true
		}
	}
	return false
}

// WeeklyCoverage compares base's nominal weekly schedule against the union of
// the reference schedules. It returns the weekly duration of base, of the union
// of references, and of their overlap. Holidays and DST are deliberately
// ignored: this is a schedule-shape comparison, and timezone offsets are
// ignored in the same way, so it is constant for a calendar set.
func WeeklyCoverage(base *Calendar, references []*Calendar) (sla, team, overlap time.Duration) {
	for day := 0; day < 7; day++ {
		baseIntervals := base.weekly[day]
		referenceIntervals := unionWeeklyMinutes(references, day)
		sla += intervalsDuration(baseIntervals)
		team += intervalsDuration(referenceIntervals)
		overlap += intervalsDuration(intersectIntervals(baseIntervals, referenceIntervals))
	}
	return sla, team, overlap
}

func unionWeeklyMinutes(references []*Calendar, day int) []minuteInterval {
	var all []minuteInterval
	for _, calendar := range references {
		all = append(all, calendar.weekly[day]...)
	}
	if len(all) == 0 {
		return nil
	}
	sort.Slice(all, func(i, j int) bool { return all[i].start < all[j].start })
	merged := all[:1]
	for _, interval := range all[1:] {
		last := &merged[len(merged)-1]
		if interval.start <= last.end {
			if interval.end > last.end {
				last.end = interval.end
			}
			continue
		}
		merged = append(merged, interval)
	}
	return merged
}

func intersectIntervals(a, b []minuteInterval) []minuteInterval {
	var result []minuteInterval
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		start := a[i].start
		if b[j].start > start {
			start = b[j].start
		}
		end := a[i].end
		if b[j].end < end {
			end = b[j].end
		}
		if end > start {
			result = append(result, minuteInterval{start: start, end: end})
		}
		if a[i].end < b[j].end {
			i++
		} else {
			j++
		}
	}
	return result
}

func intervalsDuration(intervals []minuteInterval) time.Duration {
	var total time.Duration
	for _, interval := range intervals {
		total += time.Duration(interval.end-interval.start) * time.Minute
	}
	return total
}

func sumSpans(spans []span) time.Duration {
	var total time.Duration
	for _, s := range spans {
		total += s.duration()
	}
	return total
}

func mergeSpans(spans []span) []span {
	if len(spans) == 0 {
		return nil
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })
	merged := spans[:1]
	for _, s := range spans[1:] {
		last := &merged[len(merged)-1]
		if !s.start.After(last.end) {
			if s.end.After(last.end) {
				last.end = s.end
			}
			continue
		}
		merged = append(merged, s)
	}
	return merged
}

// overlapDuration returns how much of target overlaps the merged spans.
func overlapDuration(target span, merged []span) time.Duration {
	var total time.Duration
	for _, s := range merged {
		if !s.end.After(target.start) {
			continue
		}
		if !s.start.Before(target.end) {
			break
		}
		start := s.start
		if target.start.After(start) {
			start = target.start
		}
		end := s.end
		if target.end.Before(end) {
			end = target.end
		}
		if end.After(start) {
			total += end.Sub(start)
		}
	}
	return total
}
