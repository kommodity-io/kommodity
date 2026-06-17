package api

import (
	"fmt"
	"time"
)

const (
	hoursPerDay   = 24
	daysPerMonth  = 30
	daysPerYear   = 365
	hoursPerMonth = hoursPerDay * daysPerMonth
	hoursPerYear  = hoursPerDay * daysPerYear
)

// FormatAge returns a short human-readable age string (e.g. "5m", "2h", "3d", "4mo", "1y")
// computed from the given creation timestamp. Mirrors kubectl's HumanDuration shorthand.
func FormatAge(creation time.Time) string {
	if creation.IsZero() {
		return UnknownVersion
	}

	delta := max(time.Since(creation), 0)

	switch {
	case delta < time.Minute:
		return fmt.Sprintf("%ds", int(delta.Seconds()))
	case delta < time.Hour:
		return fmt.Sprintf("%dm", int(delta.Minutes()))
	case delta < hoursPerDay*time.Hour:
		return fmt.Sprintf("%dh", int(delta.Hours()))
	case delta < daysPerMonth*hoursPerDay*time.Hour:
		return fmt.Sprintf("%dd", int(delta.Hours()/hoursPerDay))
	case delta < daysPerYear*hoursPerDay*time.Hour:
		return fmt.Sprintf("%dmo", int(delta.Hours()/hoursPerMonth))
	default:
		return fmt.Sprintf("%dy", int(delta.Hours()/hoursPerYear))
	}
}
