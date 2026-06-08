package providerio

import (
	"net/http"
	"strings"
)

type AuthHeaders struct {
	APIKey            string
	DefaultAuthHeader string
	DefaultAuthScheme string
	AuthHeader        string
	AuthScheme        string
	AuthHeaderValue   string
	CustomHeaders     map[string]string
}

func ApplyAuthHeaders(request *http.Request, options AuthHeaders) {
	for key, value := range options.CustomHeaders {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		request.Header.Set(key, strings.TrimSpace(value))
	}

	header := strings.TrimSpace(options.AuthHeader)
	customHeader := header != ""
	if header == "" {
		header = strings.TrimSpace(options.DefaultAuthHeader)
	}
	if header == "" {
		return
	}

	value := strings.TrimSpace(options.AuthHeaderValue)
	if value == "" {
		apiKey := strings.TrimSpace(options.APIKey)
		if apiKey == "" {
			return
		}
		scheme := strings.TrimSpace(options.AuthScheme)
		if !customHeader && scheme == "" {
			scheme = strings.TrimSpace(options.DefaultAuthScheme)
		}
		if scheme != "" && !strings.EqualFold(scheme, "none") && !strings.EqualFold(scheme, "raw") {
			value = scheme + " " + apiKey
		} else {
			value = apiKey
		}
	}
	request.Header.Set(header, value)
}

func CopyHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	copied := make(map[string]string, len(headers))
	for key, value := range headers {
		copied[key] = value
	}
	return copied
}
