package redaction

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
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

// ctrlGap matches C0/C1 bytes (Cc other than tab/LF/CR, plus lone Latin-1 C1)
// between characters of a secret shape. Matching stays on the original string:
// a deleted control is never a join, so \b still treats wordchar+control as a
// boundary and tokens that were never adjacent stay that way. Tab/LF/CR are
// excluded so log line structure is unchanged. \x{FFFD} lets the regexp locate
// a lone invalid UTF-8 byte; validSecretControlGaps subsequently accepts only
// raw C1 bytes and rejects a real, valid UTF-8 U+FFFD rune.
const ctrlGap = `[\x00-\x08\x0b\x0c\x0e-\x1f\x7f\x80-\x9f\x{FFFD}]*`

// ctrlLit quotes s as a regexp literal with ctrlGap strictly between runes, so
// a NUL/ESC/C1 may split the literal without letting a match end on a gap.
func ctrlLit(s string) string {
	var b strings.Builder
	b.Grow(len(s) * (1 + len(ctrlGap)))
	first := true
	for _, r := range s {
		if !first {
			b.WriteString(ctrlGap)
		}
		b.WriteString(regexp.QuoteMeta(string(r)))
		first = false
	}
	return b.String()
}

func ctrlJoin(parts ...string) string {
	return strings.Join(parts, ctrlGap)
}

// secretBody keeps gaps strictly between the minimum required body characters.
// Once an unbounded shape has reached that high-confidence minimum, its
// optional tail stays contiguous: a later control is a suffix delimiter rather
// than permission to absorb the following token into the secret (which could
// feed unrelated suffix text into the OpenAI kebab-case exception).
func secretBody(class string, minimum int, unbounded bool) string {
	if minimum <= 0 {
		return ""
	}
	quantifier := strconv.Itoa(minimum - 1)
	body := class + `(?:` + ctrlGap + class + `){` + quantifier + `}`
	if unbounded {
		body += class + `*`
	}
	return body
}

// openaiKeyPattern mirrors secrets.Scan's broad sk- body. Known OpenAI
// prefixes (sk-proj-/sk-svcacct-/sk-admin-) are always redacted; other sk-
// digit-free matches with an interior hyphen are left alone (kebab-case false
// positives), while digit-free legacy sk- credentials are still redacted.
// Applied via ReplaceAllStringFunc rather than the plain list below.
var openaiKeyPattern = regexp.MustCompile(`\b` + ctrlJoin(ctrlLit("sk-"), secretBody(`[A-Za-z0-9_-]`, 20, true)))

// textSecretPatterns mirror secrets.Scan for end-boundary behavior and the
// shared high-confidence shapes. A leading \b keeps each pattern from firing
// mid-word; a trailing \b is omitted so a secret followed by more word
// characters outside its body class (e.g. AKIA…EXAMPLEEXTRA) still matches,
// and a secret that ends in "-" (allowed by some body classes) is fully
// redacted rather than leaving the hyphen behind. glpat is redaction-only
// (not in secrets.Scan); ASIA temporary access keys are kept alongside AKIA.
// openai keys are handled separately (digit filter). JWT has a strict form
// (both segments start with eyJ) and a looser three-segment form.
// ctrlGap between shape characters keeps NUL/ESC/C1 split secrets matching
// without stripping those bytes out of the subject first.
var textSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\b` + ctrlLit("sk-ant-") + ctrlGap + `(?:` + ctrlJoin(ctrlLit("api"), `\d`, `\d`, `-`) + ctrlGap + `)?` + secretBody(`[A-Za-z0-9_-]`, 20, true)),
	regexp.MustCompile(`\b` + ctrlJoin(ctrlLit("github_pat_"), secretBody(`[A-Za-z0-9_]`, 22, true))),
	regexp.MustCompile(`\b` + ctrlJoin(ctrlLit("gh"), `[pousr]`, `_`, secretBody(`[A-Za-z0-9]`, 36, true))),
	regexp.MustCompile(`\b` + ctrlJoin(ctrlLit("glpat-"), secretBody(`[A-Za-z0-9_-]`, 12, true))),
	regexp.MustCompile(`\b` + ctrlJoin(ctrlLit("AIza"), secretBody(`[0-9A-Za-z\-_]`, 35, true))),
	regexp.MustCompile(`\b` + ctrlJoin(ctrlLit("xox"), `[baprs]`, `-`, secretBody(`[A-Za-z0-9-]`, 10, true))),
	regexp.MustCompile(`\b` + ctrlJoin(`(?:`+ctrlLit("AKIA")+`|`+ctrlLit("ASIA")+`)`, secretBody(`[A-Z0-9]`, 16, false))),
	regexp.MustCompile(`\b` + ctrlJoin(ctrlLit("eyJ"), secretBody(`[A-Za-z0-9_-]`, 10, true), `\.`, ctrlLit("eyJ"), secretBody(`[A-Za-z0-9_-]`, 10, true), `\.`, secretBody(`[A-Za-z0-9_-]`, 10, true))),
	regexp.MustCompile(`\b` + ctrlJoin(ctrlLit("eyJ"), secretBody(`[A-Za-z0-9_-]`, 10, true), `\.`, secretBody(`[A-Za-z0-9_-]`, 10, true), `\.`, secretBody(`[A-Za-z0-9_-]`, 10, true))),
}

var (
	privateKeyPattern = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
	jsonStringPattern = regexp.MustCompile(`("([^"\\]*(?:\\.[^"\\]*)*)"\s*:\s*)"([^"\\]*(?:\\.[^"\\]*)*)"`)
	assignPattern     = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_.-]*)(\s*=\s*)(?:"([^"]*)"|'([^']*)'|([^\s&]+))`)
	// Redact the ENTIRE credential after the scheme (to end of line), not just the
	// first token: parameterized schemes (Digest, OAuth, AWS4-HMAC-SHA256) spread
	// the secret across comma-separated params (…, response=…, Signature=…), so a
	// single-token capture would leave the actual secret visible. A known scheme is
	// kept for readability; the scheme is OPTIONAL so an opaque or custom-scheme
	// credential (no recognized scheme) still has its whole value redacted (M12).
	headerPattern = regexp.MustCompile(`(?i)\b(authorization|proxy-authorization)\s*:\s*(?:(bearer|basic|token|apikey|api-key|digest|negotiate|oauth|aws4-hmac-sha256)\s+)?([^\r\n]+)`)
	secretHeader  = regexp.MustCompile(`(?i)\b(x-api-key|api-key|cookie|set-cookie)\s*:\s*([^\r\n]+)`)
	queryPattern  = regexp.MustCompile(`([?&])([^=&#\s]+)=([^&#\s]+)`)
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
	return keyLooksSensitive(normalized)
}

// secretKeySegments are "_"-delimited segments that mark the whole key sensitive
// even when the full name isn't in the exact list, so compound names like
// db_password, session_secret, and stripe_secret_key are caught. "token" is
// handled separately (suffix-only, in keyLooksSensitive) so the agent's many
// token-COUNT fields (max_tokens, prompt_tokens, token_count) are never redacted.
var secretKeySegments = map[string]struct{}{
	"password":    {},
	"passwd":      {},
	"passphrase":  {},
	"secret":      {},
	"credential":  {},
	"credentials": {},
	"apikey":      {},
}

// keyLooksSensitive applies conservative structural heuristics to a normalized
// key (already lower-cased and "_"-delimited by normalizeKey). It is deliberately
// narrow: a bare "token"/"key" segment is NOT enough, so token-count and ordinary
// "*_key" fields (max_tokens, primary_key, public_key) stay un-redacted.
func keyLooksSensitive(normalized string) bool {
	segments := strings.Split(normalized, "_")
	for i, seg := range segments {
		if _, ok := secretKeySegments[seg]; ok {
			return true
		}
		// "<x>_token" (singular, trailing) is a credential — auth_token, csrf_token,
		// vault_token. NOT "tokens" (plural count) and NOT "token_<x>" (token_count,
		// token_usage), where token is pluralized or not the trailing segment.
		if seg == "token" && i > 0 && i == len(segments)-1 {
			return true
		}
		// "api_key" / "private_key" as adjacent segments. A bare "key" stays
		// non-sensitive (primary_key, public_key, cache_key, foreign_key, …).
		if seg == "key" && i > 0 {
			switch segments[i-1] {
			case "api", "private":
				return true
			}
		}
	}
	return false
}

// stripControlBytes removes C0/C1 controls (Cc other than tab, LF, and CR).
// Used to normalize an already-matched secret so prefix/digit checks see the
// rejoined shape. It is matching-time only and must not be applied to
// RedactString's input or return value. Tab/LF/CR stay. Lone Latin-1 C1 bytes
// (0x80–0x9F, invalid UTF-8) are stripped too; UTF-8 continuation bytes are
// not, because they are not controls.
func stripControlBytes(s string) string {
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x80 {
			if c != '\t' && c != '\n' && c != '\r' && (c < 0x20 || c == 0x7F) {
				return stripControlBytesFrom(s, i)
			}
			i++
			continue
		}
		if c <= 0x9F {
			// 0x80–0x9F at a rune boundary is a lone C1 byte, not UTF-8.
			return stripControlBytesFrom(s, i)
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if unicode.IsControl(r) {
			return stripControlBytesFrom(s, i)
		}
		i += size
	}
	return s
}

func stripControlBytesFrom(s string, start int) string {
	var b strings.Builder
	b.Grow(len(s))
	b.WriteString(s[:start])
	for i := start; i < len(s); {
		c := s[i]
		if c < 0x80 {
			if c != '\t' && c != '\n' && c != '\r' && (c < 0x20 || c == 0x7F) {
				i++
				continue
			}
			b.WriteByte(c)
			i++
			continue
		}
		if c <= 0x9F {
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if unicode.IsControl(r) {
			i += size
			continue
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

// validSecretControlGaps disambiguates RuneError matches at the byte boundary.
// Go's regexp engine represents both a malformed single byte and a legitimate
// U+FFFD rune as RuneError. Only raw invalid C1 bytes (0x80-0x9F) are supported
// gaps; a valid UTF-8 replacement rune, or another malformed byte, must keep
// the candidate split and prevent redaction.
func validSecretControlGaps(match string) bool {
	for i := 0; i < len(match); {
		r, size := utf8.DecodeRuneInString(match[i:])
		if r != utf8.RuneError {
			i += size
			continue
		}
		if size != 1 || match[i] < 0x80 || match[i] > 0x9F {
			return false
		}
		i++
	}
	return true
}

func RedactString(value string, options Options) string {
	replacement := replacement(options)
	// Match on the original string. Shape patterns allow C0/C1 gaps between
	// characters so a split secret still matches; stripping first would join
	// tokens that were never adjacent and make \b miss a leading wordchar.
	redacted := value
	if len(options.ExtraSecretValues) > 0 {
		secrets := append([]string{}, options.ExtraSecretValues...)
		sort.SliceStable(secrets, func(i, j int) bool {
			return len(secrets[i]) > len(secrets[j])
		})
		for _, secret := range secrets {
			if strings.TrimSpace(secret) != "" {
				redacted = strings.ReplaceAll(redacted, secret, replacement)
			}
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
	redacted = headerPattern.ReplaceAllStringFunc(redacted, func(match string) string {
		groups := headerPattern.FindStringSubmatch(match)
		// groups[2] is the known scheme (kept for readability) or "" for an opaque /
		// custom-scheme credential — in which case the whole value is redacted (M12).
		if groups[2] != "" {
			return groups[1] + ": " + groups[2] + " " + replacement
		}
		return groups[1] + ": " + replacement
	})
	redacted = secretHeader.ReplaceAllString(redacted, "$1: "+replacement)
	redacted = redactURLPasswords(redacted, replacement)
	redacted = queryPattern.ReplaceAllStringFunc(redacted, func(match string) string {
		parts := queryPattern.FindStringSubmatch(match)
		if len(parts) < 4 || !IsSensitiveKey(parts[2], options) {
			return match
		}
		return parts[1] + parts[2] + "=" + replacement
	})
	// Match high-confidence specialized shapes first. In particular, the broad
	// sk- pattern may reach its minimum before a control inside a longer
	// Anthropic key; letting the Anthropic shape consume that split first avoids
	// leaving a recognizable credential suffix behind.
	for _, pattern := range textSecretPatterns {
		redacted = pattern.ReplaceAllStringFunc(redacted, func(match string) string {
			if !validSecretControlGaps(match) {
				return match
			}
			return replacement
		})
	}
	// Apply the broad OpenAI shape after specialized keys so its kebab-case
	// false-positive filter considers only the matched key, never suffix text.
	redacted = openaiKeyPattern.ReplaceAllStringFunc(redacted, func(match string) string {
		if !validSecretControlGaps(match) {
			return match
		}
		normalized := stripControlBytes(match)
		if !knownOpenAIKeyPrefix(normalized) && !secretMatchHasDigit(normalized) &&
			strings.Contains(strings.TrimPrefix(normalized, "sk-"), "-") {
			return match
		}
		return replacement
	})
	return redacted
}

// knownOpenAIKeyPrefix is the redaction-side twin of secrets.knownOpenAIKeyPrefix:
// known OpenAI-issued forms redact even with an alphabet-only body.
func knownOpenAIKeyPrefix(match string) bool {
	return strings.HasPrefix(match, "sk-proj-") ||
		strings.HasPrefix(match, "sk-svcacct-") ||
		strings.HasPrefix(match, "sk-admin-")
}

// secretMatchHasDigit is the redaction-side twin of secrets.containsDigit.
func secretMatchHasDigit(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
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
		// Track only the current DFS path: drop the pointer after recursing so a
		// shared (non-cyclic) reference reached again via a SIBLING branch is not
		// mistaken for a cycle. Only an ancestor still on the path triggers it.
		out := redactReflect(value.Elem(), context, depth+1)
		delete(context.seen, ptr)
		return out
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
		delete(context.seen, ptr)
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

var urlWithCredsPattern = regexp.MustCompile(`\b(?:https?|wss?|ftp)://[^\s]+`)

func redactURLPasswords(value string, replacement string) string {
	return urlWithCredsPattern.ReplaceAllStringFunc(value, func(candidate string) string {
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
