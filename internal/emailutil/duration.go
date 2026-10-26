package emailutil

import (
	"fmt"
	"time"
)

// HumanizeDuration renders an email expiry window as plain words: "30 minutes",
// "24 hours", "7 days". Transactional templates use it so the validity stated
// in the email is derived from the same constant that enforces it, instead of
// drifting from a hand-written number.
func HumanizeDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		return pluralize(int(d/(24*time.Hour)), "day")
	case d >= time.Hour && d%time.Hour == 0:
		return pluralize(int(d/time.Hour), "hour")
	case d >= time.Minute && d%time.Minute == 0:
		return pluralize(int(d/time.Minute), "minute")
	default:
		return d.String()
	}
}

func pluralize(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
