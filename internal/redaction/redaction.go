package redaction

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"unicode"
)

const (
	RedactedSecret    = "[REDACTED]"
	CircularReference = "[Circular]"
	maxDepthDefault   = 16
)

type Options struct {
	Replacement        string
	ExtraSensitiveKeys []string
	ExtraSecretValues  []string
	MaxDepth           int
}

type RedactedError struct {
	Name    string         `json:"name"`
	Message string         `json:"message"`
	Stack   string         `json:"stack,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
}

var sensitiveKeys = map[string]struct{}{
	"access_token":          {},
	"anthropic_api_key":     {},
	"api_key":               {},
	"apikey":                {},
	"auth_token":            {},
	"authorization":         {},
	"aws_secret_access_key": {},
	"aws_session_token":     {},
	"bearer":                {},
	"bearer_token":          {},
	"client_secret":         {},
	"cookie":                {},
	"credential":            {},
	"credentials":           {},
	"gemini_api_key":        {},
	"github_token":          {},
	"gitlab_token":          {},
	"google_api_key":        {},
	"id_token":              {},
	"jwt":                   {},
	"npm_token":             {},
	"oauth_token":           {},
	"openai_api_key":        {},
	"passphrase":            {},
	"password":              {},
	"private_key":           {},
	"proxy_authorization":   {},
	"refresh_token":         {},
	"secret":                {},
	"session_token":         {},
	"set_cookie":            {},
	"token":                 {},
	"x_api_key":             {},
	"zero_api_key":          {},
}

var textSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9._-]{12,}\b`),
	regexp.MustCompile(`\bsk-ant-api\d{2}-[A-Za-z0-9._-]{12,}\b`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{12,}\b`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{12,}\b`),
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{12,}\b`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{12,}\b`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{12,}\b`),
	regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`),
}

var (
	privateKeyPattern = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
	jsonStringPattern = regexp.MustCompile(`("([^"\\]*(?:\\.[^"\\]*)*)"\s*:\s*)"([^"\\]*(?:\\.[^"\\]*)*)"`)
	assignPattern     = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_.-]*)(\s*=\s*)(?:"([^"]*)"|'([^']*)'|([^\s&]+))`)
	headerPattern     = regexp.MustCompile(`(?i)\b(authorization|proxy-authorization)\s*:\s*(bearer|basic)\s+([^\s,;]+)`)
	secretHeader      = regexp.MustCompile(`(?i)\b(x-api-key|api-key|cookie|set-cookie)\s*:\s*([^\r\n]+)`)
	queryPattern      = regexp.MustCompile(`([?&])([^=&#\s]+)=([^&#\s]+)`)
)

func IsSensitiveKey(key string, options Options) bool {
	normalized := normalizeKey(key)
	if normalized == "" {
		return false
	}
	if _, ok := sensitiveKeys[normalized]; ok {
		return true
	}
	for _, extra := range options.ExtraSensitiveKeys {
		if normalizeKey(extra) == normalized {
			return true
		}
	}
	return false
}

func RedactString(value string, options Options) string {
	replacement := replacement(options)
	redacted := value
	for _, secret := range options.ExtraSecretValues {
		if strings.TrimSpace(secret) != "" {
			redacted = strings.ReplaceAll(redacted, secret, replacement)
		}
	}

	redacted = privateKeyPattern.ReplaceAllString(redacted, replacement)
	redacted = jsonStringPattern.ReplaceAllStringFunc(redacted, func(match string) string {
		parts := jsonStringPattern.FindStringSubmatch(match)
		if len(parts) < 3 || !IsSensitiveKey(parts[2], options) {
			return match
		}
		return parts[1] + `"` + replacement + `"`
	})
	redacted = assignPattern.ReplaceAllStringFunc(redacted, func(match string) string {
		parts := assignPattern.FindStringSubmatch(match)
		if len(parts) < 6 || !IsSensitiveKey(parts[1], options) {
			return match
		}
		if parts[3] != "" {
			return parts[1] + parts[2] + `"` + replacement + `"`
		}
		if parts[4] != "" {
			return parts[1] + parts[2] + `'` + replacement + `'`
		}
		return parts[1] + parts[2] + replacement
	})
	redacted = headerPattern.ReplaceAllString(redacted, "$1: $2 "+replacement)
	redacted = secretHeader.ReplaceAllString(redacted, "$1: "+replacement)
	redacted = redactURLPasswords(redacted, replacement)
	redacted = queryPattern.ReplaceAllStringFunc(redacted, func(match string) string {
		parts := queryPattern.FindStringSubmatch(match)
		if len(parts) < 4 || !IsSensitiveKey(parts[2], options) {
			return match
		}
		return parts[1] + parts[2] + "=" + replacement
	})
	for _, pattern := range textSecretPatterns {
		redacted = pattern.ReplaceAllString(redacted, replacement)
	}
	return redacted
}

func RedactValue(value any, options Options) any {
	return redactReflect(reflect.ValueOf(value), redactionContext{
		options:     options,
		replacement: replacement(options),
		maxDepth:    maxDepth(options),
		seen:        map[uintptr]struct{}{},
	}, 0)
}

func RedactError(err error, options Options) RedactedError {
	if err == nil {
		return RedactedError{Name: "Error", Message: ""}
	}
	redacted := RedactedError{
		Name:    errorName(err),
		Message: RedactString(err.Error(), options),
	}
	fields := exportedFields(reflect.ValueOf(err), options)
	if len(fields) > 0 {
		redacted.Fields = fields
	}
	var stackTracer interface{ StackTrace() fmt.Stringer }
	if errors.As(err, &stackTracer) {
		redacted.Stack = RedactString(stackTracer.StackTrace().String(), options)
	}
	return redacted
}

func ErrorMessage(err error, options Options) string {
	return RedactError(err, options).Message
}

type redactionContext struct {
	options     Options
	replacement string
	maxDepth    int
	seen        map[uintptr]struct{}
}

func redactReflect(value reflect.Value, context redactionContext, depth int) any {
	if !value.IsValid() {
		return nil
	}
	for value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if depth >= context.maxDepth {
		return "[MaxDepth]"
	}

	switch value.Kind() {
	case reflect.String:
		return RedactString(value.String(), context.options)
	case reflect.Bool:
		return value.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return value.Uint()
	case reflect.Float32, reflect.Float64:
		return value.Float()
	case reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		ptr := value.Pointer()
		if _, ok := context.seen[ptr]; ok {
			return CircularReference
		}
		context.seen[ptr] = struct{}{}
		return redactReflect(value.Elem(), context, depth+1)
	case reflect.Map:
		if value.IsNil() {
			return nil
		}
		ptr := value.Pointer()
		if _, ok := context.seen[ptr]; ok {
			return CircularReference
		}
		context.seen[ptr] = struct{}{}
		out := make(map[string]any, value.Len())
		iter := value.MapRange()
		for iter.Next() {
			key := fmt.Sprint(redactReflect(iter.Key(), context, depth+1))
			if IsSensitiveKey(key, context.options) {
				out[key] = context.replacement
				continue
			}
			out[key] = redactReflect(iter.Value(), context, depth+1)
		}
		return out
	case reflect.Slice, reflect.Array:
		out := make([]any, value.Len())
		for index := 0; index < value.Len(); index++ {
			out[index] = redactReflect(value.Index(index), context, depth+1)
		}
		return out
	case reflect.Struct:
		if value.CanInterface() {
			if err, ok := value.Interface().(error); ok {
				return RedactError(err, context.options)
			}
		}
		out := make(map[string]any, value.NumField())
		valueType := value.Type()
		for index := 0; index < value.NumField(); index++ {
			field := valueType.Field(index)
			if field.PkgPath != "" {
				continue
			}
			name := field.Name
			if tag := field.Tag.Get("json"); tag != "" {
				name = strings.Split(tag, ",")[0]
				if name == "-" {
					continue
				}
			}
			if IsSensitiveKey(name, context.options) {
				out[name] = context.replacement
				continue
			}
			out[name] = redactReflect(value.Field(index), context, depth+1)
		}
		return out
	default:
		if value.CanInterface() {
			return value.Interface()
		}
		return fmt.Sprint(value)
	}
}

func exportedFields(value reflect.Value, options Options) map[string]any {
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return nil
	}
	fields := map[string]any{}
	context := redactionContext{
		options:     options,
		replacement: replacement(options),
		maxDepth:    maxDepth(options),
		seen:        map[uintptr]struct{}{},
	}
	valueType := value.Type()
	for index := 0; index < value.NumField(); index++ {
		field := valueType.Field(index)
		if field.PkgPath != "" {
			continue
		}
		name := field.Name
		if IsSensitiveKey(name, options) {
			fields[name] = context.replacement
		} else {
			fields[name] = redactReflect(value.Field(index), context, 1)
		}
	}
	return fields
}

func errorName(err error) string {
	name := reflect.TypeOf(err).String()
	if name == "" {
		return "Error"
	}
	return name
}

func replacement(options Options) string {
	if options.Replacement != "" {
		return options.Replacement
	}
	return RedactedSecret
}

func maxDepth(options Options) int {
	if options.MaxDepth > 0 {
		return options.MaxDepth
	}
	return maxDepthDefault
}

func normalizeKey(key string) string {
	key = strings.TrimSpace(key)
	var builder strings.Builder
	var lastUnderscore bool
	for _, r := range key {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(unicode.ToLower(r))
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(builder.String(), "_")
}

func redactURLPasswords(value string, replacement string) string {
	return regexp.MustCompile(`\b(?:https?|wss?|ftp)://[^\s]+`).ReplaceAllStringFunc(value, func(candidate string) string {
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.User == nil {
			return candidate
		}
		if _, hasPassword := parsed.User.Password(); !hasPassword {
			return candidate
		}
		parsed.User = url.UserPassword(parsed.User.Username(), replacement)
		return parsed.String()
	})
}
