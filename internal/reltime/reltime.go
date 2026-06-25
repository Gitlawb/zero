package reltime

import "fmt"

// RelTime renders a duration in seconds as a human-readable relative-time
// string. This is the ORIGINAL implementation, kept verbatim so
// characterization tests can be pinned against it before refactoring.
func RelTime(d int) string {
	if d < 0 {
		d = -d
		if d < 60 {
			if d == 1 {
				return "1 second ago"
			}
			return fmt.Sprintf("%d seconds ago", d)
		}
		if d < 3600 {
			m := d / 60
			if m == 1 {
				return "1 minute ago"
			}
			return fmt.Sprintf("%d minutes ago", m)
		}
		if d < 86400 {
			h := d / 3600
			if h == 1 {
				return "1 hour ago"
			}
			return fmt.Sprintf("%d hours ago", h)
		}
		x := d / 86400
		if x == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", x)
	} else if d == 0 {
		return "just now"
	} else {
		if d < 60 {
			if d == 1 {
				return "in 1 second"
			}
			return fmt.Sprintf("in %d seconds", d)
		}
		if d < 3600 {
			m := d / 60
			if m == 1 {
				return "in 1 minute"
			}
			return fmt.Sprintf("in %d minutes", m)
		}
		if d < 86400 {
			h := d / 3600
			if h == 1 {
				return "in 1 hour"
			}
			return fmt.Sprintf("in %d hours", h)
		}
		x := d / 86400
		if x == 1 {
			return "in 1 day"
		}
		return fmt.Sprintf("in %d days", x)
	}
}
