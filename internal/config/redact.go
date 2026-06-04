package config

import (
	"regexp"
	"strings"
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9._-]+`),
	regexp.MustCompile(`(?i)(api[_-]?key["'=:\s]+)[^"',\s)]+`),
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._-]+`),
}

func redactSecrets(value string, knownSecrets ...string) string {
	redacted := value
	for _, secret := range knownSecrets {
		if secret == "" {
			continue
		}
		redacted = strings.ReplaceAll(redacted, secret, "[REDACTED]")
	}
	for _, pattern := range secretPatterns {
		redacted = pattern.ReplaceAllStringFunc(redacted, func(match string) string {
			for _, prefix := range []string{"apiKey=", "api_key=", "api-key=", "Bearer "} {
				if strings.HasPrefix(strings.ToLower(match), strings.ToLower(prefix)) {
					return match[:len(prefix)] + "[REDACTED]"
				}
			}
			if strings.HasPrefix(match, "sk-") {
				return "[REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return redacted
}
