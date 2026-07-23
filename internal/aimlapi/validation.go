package aimlapi

import (
	"net/mail"
	"strings"
)

// ValidEmail performs the lightweight validation used before starting the
// passwordless onboarding request.
func ValidEmail(value string) bool {
	value = strings.TrimSpace(value)
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value {
		return false
	}
	at := strings.LastIndexByte(value, '@')
	if at <= 0 || at == len(value)-1 {
		return false
	}
	domain := value[at+1:]
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.Contains(domain, "..") {
		return false
	}
	dot := strings.LastIndexByte(domain, '.')
	if dot <= 0 || dot == len(domain)-1 {
		return false
	}
	tld := domain[dot+1:]
	if len(tld) < 2 {
		return false
	}
	for _, char := range tld {
		if char < 'A' || char > 'Z' && char < 'a' || char > 'z' {
			return false
		}
	}
	return true
}
