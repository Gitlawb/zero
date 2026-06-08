package specmode

import (
	"strings"
)

const maxSlugLength = 60

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			builder.WriteRune(char)
			lastDash = false
		default:
			if builder.Len() > 0 && !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
		if builder.Len() >= maxSlugLength {
			break
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		return "spec"
	}
	return slug
}
