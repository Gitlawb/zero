package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/redaction"
)

const (
	defaultWebFetchMaxBytes = 64 * 1024
	maxWebFetchMaxBytes     = 512 * 1024
	webFetchTimeout         = 10 * time.Second
	webFetchRedirectLimit   = 5
	webFetchErrorBodyLimit  = 4 * 1024
)

type webFetchTool struct {
	baseTool
	client   *http.Client
	resolver webFetchResolver
}

type webFetchResolver interface {
	LookupNetIP(ctx context.Context, network string, host string) ([]netip.Addr, error)
}

type webFetchDialer interface {
	DialContext(ctx context.Context, network string, address string) (net.Conn, error)
}

type defaultWebFetchResolver struct{}

func (defaultWebFetchResolver) LookupNetIP(ctx context.Context, network string, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, network, host)
}

type webFetchBlockedPrefix struct {
	prefix netip.Prefix
	reason string
}

type webFetchEmbeddedIPv4Prefix struct {
	prefix     netip.Prefix
	byteOffset int
}

var webFetchBlockedAddrPrefixes = []webFetchBlockedPrefix{
	{prefix: netip.MustParsePrefix("0.0.0.0/8"), reason: "special-use hosts are blocked"},
	{prefix: netip.MustParsePrefix("10.0.0.0/8"), reason: "private network hosts are blocked"},
	{prefix: netip.MustParsePrefix("100.64.0.0/10"), reason: "special-use hosts are blocked"},
	{prefix: netip.MustParsePrefix("127.0.0.0/8"), reason: "loopback hosts are blocked"},
	{prefix: netip.MustParsePrefix("169.254.0.0/16"), reason: "link-local hosts are blocked"},
	{prefix: netip.MustParsePrefix("172.16.0.0/12"), reason: "private network hosts are blocked"},
	{prefix: netip.MustParsePrefix("192.0.0.0/24"), reason: "special-use hosts are blocked"},
	{prefix: netip.MustParsePrefix("192.0.2.0/24"), reason: "documentation hosts are blocked"},
	{prefix: netip.MustParsePrefix("192.88.99.0/24"), reason: "special-use hosts are blocked"},
	{prefix: netip.MustParsePrefix("192.168.0.0/16"), reason: "private network hosts are blocked"},
	{prefix: netip.MustParsePrefix("198.18.0.0/15"), reason: "benchmark network hosts are blocked"},
	{prefix: netip.MustParsePrefix("198.51.100.0/24"), reason: "documentation hosts are blocked"},
	{prefix: netip.MustParsePrefix("203.0.113.0/24"), reason: "documentation hosts are blocked"},
	{prefix: netip.MustParsePrefix("224.0.0.0/4"), reason: "multicast hosts are blocked"},
	{prefix: netip.MustParsePrefix("240.0.0.0/4"), reason: "special-use hosts are blocked"},
	{prefix: netip.MustParsePrefix("::/128"), reason: "unspecified hosts are blocked"},
	{prefix: netip.MustParsePrefix("::1/128"), reason: "loopback hosts are blocked"},
	{prefix: netip.MustParsePrefix("100::/64"), reason: "special-use hosts are blocked"},
	{prefix: netip.MustParsePrefix("2001::/23"), reason: "special-use hosts are blocked"},
	{prefix: netip.MustParsePrefix("2001:2::/48"), reason: "benchmark network hosts are blocked"},
	{prefix: netip.MustParsePrefix("2001:db8::/32"), reason: "documentation hosts are blocked"},
	{prefix: netip.MustParsePrefix("fc00::/7"), reason: "private network hosts are blocked"},
	{prefix: netip.MustParsePrefix("fe80::/10"), reason: "link-local hosts are blocked"},
	{prefix: netip.MustParsePrefix("ff00::/8"), reason: "multicast hosts are blocked"},
}

var webFetchEmbeddedIPv4Prefixes = []webFetchEmbeddedIPv4Prefix{
	{prefix: netip.MustParsePrefix("::/96"), byteOffset: 12},
	{prefix: netip.MustParsePrefix("64:ff9b::/96"), byteOffset: 12},
	{prefix: netip.MustParsePrefix("64:ff9b:1::/48"), byteOffset: 6},
	{prefix: netip.MustParsePrefix("2002::/16"), byteOffset: 2},
}

func NewWebFetchTool() Tool {
	return newWebFetchToolWithClientAndResolver(nil, defaultWebFetchResolver{})
}

func newWebFetchToolWithClient(client *http.Client) Tool {
	return newWebFetchToolWithClientAndResolver(client, nil)
}

func newWebFetchToolWithClientAndResolver(client *http.Client, resolver webFetchResolver) Tool {
	if client == nil {
		client = &http.Client{Timeout: webFetchTimeout}
	}
	return webFetchTool{
		baseTool: baseTool{
			name:        "web_fetch",
			description: "Fetch text from an http or https URL after network permission is granted.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"url": {
						Type:        "string",
						Description: "HTTP or HTTPS URL to fetch.",
					},
					"max_bytes": {
						Type:        "integer",
						Description: "Maximum response body bytes to return.",
						Default:     defaultWebFetchMaxBytes,
						Minimum:     intPtr(1),
						Maximum:     intPtr(maxWebFetchMaxBytes),
					},
				},
				Required:             []string{"url"},
				AdditionalProperties: false,
			},
			safety: Safety{
				SideEffect:      SideEffectNetwork,
				Permission:      PermissionPrompt,
				Reason:          "Fetches remote URL content over the network.",
				AdvertiseInAuto: true,
			},
		},
		client:   client,
		resolver: resolver,
	}
}

func (tool webFetchTool) Run(ctx context.Context, args map[string]any) Result {
	rawURL, err := stringArg(args, "url", "", true)
	if err != nil {
		return errorResult("Error: Invalid arguments for web_fetch: " + err.Error())
	}
	maxBytes, err := intArg(args, "max_bytes", defaultWebFetchMaxBytes, 1, maxWebFetchMaxBytes)
	if err != nil {
		return errorResult("Error: Invalid arguments for web_fetch: " + err.Error())
	}

	parsedURL, err := validateWebFetchURL(ctx, rawURL, tool.resolver)
	if err != nil {
		return errorResult("Error: Unsafe URL for web_fetch: " + err.Error())
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return errorResult("Error: Invalid URL for web_fetch: " + err.Error())
	}
	request.Header.Set("User-Agent", "zero-web-fetch/0.1")
	request.Header.Set("Accept", "text/*, application/json, application/xhtml+xml, application/xml;q=0.9, */*;q=0.5")

	client := tool.clientForRun()
	response, err := client.Do(request)
	if err != nil {
		return errorResult("Error fetching URL: " + redactWebFetchText(err.Error()))
	}
	if response.Body == nil {
		response.Body = io.NopCloser(strings.NewReader(""))
	}
	defer response.Body.Close()

	finalURL := redactWebFetchURL(parsedURL)
	if response.Request != nil && response.Request.URL != nil {
		finalURL = redactWebFetchURL(response.Request.URL)
	}
	contentType := redactWebFetchText(response.Header.Get("Content-Type"))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _, _ := readWebFetchBody(response.Body, min(maxBytes, webFetchErrorBodyLimit))
		message := fmt.Sprintf("Error fetching URL: HTTP %s", webFetchStatusLine(response))
		if strings.TrimSpace(body) != "" {
			message += "\n\n" + redactWebFetchText(body)
		}
		return Result{
			Status: StatusError,
			Output: message,
			Meta: map[string]string{
				"url":          finalURL,
				"status_code":  strconv.Itoa(response.StatusCode),
				"content_type": contentType,
			},
		}
	}

	body, truncated, err := readWebFetchBody(response.Body, maxBytes)
	if err != nil {
		return errorResult("Error reading response body: " + redactWebFetchText(err.Error()))
	}
	body = redactWebFetchText(body)

	output := strings.Join([]string{
		"URL: " + finalURL,
		"Status: " + webFetchStatusLine(response),
		"Content-Type: " + firstNonEmptyString(contentType, "unknown"),
		"Bytes: " + strconv.Itoa(len(body)),
		"",
		body,
	}, "\n")

	return Result{
		Status:    StatusOK,
		Output:    output,
		Truncated: truncated,
		Meta: map[string]string{
			"url":          finalURL,
			"status_code":  strconv.Itoa(response.StatusCode),
			"content_type": contentType,
			"bytes":        strconv.Itoa(len(body)),
			"truncated":    strconv.FormatBool(truncated),
		},
	}
}

func (tool webFetchTool) clientForRun() http.Client {
	if tool.client == nil {
		return http.Client{
			Timeout:       webFetchTimeout,
			Transport:     webFetchSafeTransport(nil, tool.resolver),
			CheckRedirect: webFetchRedirectPolicy(nil, tool.resolver),
		}
	}
	client := *tool.client
	client.Transport = webFetchSafeTransport(client.Transport, tool.resolver)
	client.CheckRedirect = webFetchRedirectPolicy(tool.client.CheckRedirect, tool.resolver)
	return client
}

func webFetchSafeTransport(roundTripper http.RoundTripper, resolver webFetchResolver) http.RoundTripper {
	var transport *http.Transport
	switch typed := roundTripper.(type) {
	case nil:
		transport = http.DefaultTransport.(*http.Transport).Clone()
	case *http.Transport:
		transport = typed.Clone()
	default:
		return roundTripper
	}

	dialer := &net.Dialer{Timeout: webFetchTimeout, KeepAlive: 30 * time.Second}
	transport.Proxy = nil
	transport.DialContext = webFetchSafeDialContext(resolver, dialer)
	transport.DialTLSContext = nil
	return transport
}

func webFetchSafeDialContext(resolver webFetchResolver, dialer webFetchDialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network string, address string) (net.Conn, error) {
		pinnedAddress, err := webFetchSafeDialAddress(ctx, resolver, address)
		if err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, pinnedAddress)
	}
}

func webFetchSafeDialAddress(ctx context.Context, resolver webFetchResolver, address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("validate dial target: %w", err)
	}
	addrs, err := resolveWebFetchHostAddrs(ctx, host, firstWebFetchResolver(resolver), true)
	if err != nil {
		return "", err
	}
	if len(addrs) == 0 {
		return "", fmt.Errorf("host did not resolve to an IP address")
	}
	return net.JoinHostPort(addrs[0].String(), port), nil
}

func webFetchRedirectPolicy(previous func(*http.Request, []*http.Request) error, resolver webFetchResolver) func(*http.Request, []*http.Request) error {
	return func(request *http.Request, via []*http.Request) error {
		if len(via) >= webFetchRedirectLimit {
			return fmt.Errorf("too many redirects: maximum is %d", webFetchRedirectLimit)
		}
		if err := validateParsedWebFetchURL(request.Context(), request.URL, resolver); err != nil {
			return fmt.Errorf("Unsafe redirect URL: %w", err)
		}
		if previous != nil {
			return previous(request, via)
		}
		return nil
	}
}

func readWebFetchBody(body io.Reader, maxBytes int) (string, bool, error) {
	raw, err := io.ReadAll(io.LimitReader(body, int64(maxBytes)+1))
	if err != nil {
		return "", false, err
	}
	truncated := len(raw) > maxBytes
	if truncated {
		raw = raw[:maxBytes]
	}
	return string(raw), truncated, nil
}

func validateWebFetchURL(ctx context.Context, rawURL string, resolver webFetchResolver) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("url is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if err := validateParsedWebFetchURL(ctx, parsed, resolver); err != nil {
		return nil, err
	}
	return parsed, nil
}

func validateParsedWebFetchURL(ctx context.Context, parsed *url.URL, resolver webFetchResolver) error {
	if parsed == nil {
		return fmt.Errorf("url is required")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("only http and https URLs are supported")
	}
	if parsed.User != nil {
		return fmt.Errorf("URLs with user info are not allowed")
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("URL host is required")
	}
	if strings.Contains(host, "%") {
		return fmt.Errorf("URL host zones are not allowed")
	}
	if err := validateWebFetchPort(parsed); err != nil {
		return err
	}
	if err := rejectUnsafeWebFetchHost(ctx, host, resolver); err != nil {
		return err
	}
	return nil
}

func validateWebFetchPort(parsed *url.URL) error {
	port := parsed.Port()
	if port == "" {
		return nil
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http":
		if port == "80" {
			return nil
		}
	case "https":
		if port == "443" {
			return nil
		}
	}
	return fmt.Errorf("only default ports are allowed for %s URLs", parsed.Scheme)
}

func rejectUnsafeWebFetchHost(ctx context.Context, host string, resolver webFetchResolver) error {
	_, err := resolveWebFetchHostAddrs(ctx, host, resolver, false)
	return err
}

func resolveWebFetchHostAddrs(ctx context.Context, host string, resolver webFetchResolver, requireResolver bool) ([]netip.Addr, error) {
	normalized := strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if normalized == "" {
		return nil, fmt.Errorf("URL host is required")
	}
	switch {
	case normalized == "localhost" || strings.HasSuffix(normalized, ".localhost"):
		return nil, fmt.Errorf("localhost hosts are blocked")
	case normalized == "metadata" || normalized == "metadata.google.internal":
		return nil, fmt.Errorf("metadata service hosts are blocked")
	case strings.HasSuffix(normalized, ".local"):
		return nil, fmt.Errorf("local network hosts are blocked")
	}

	addr, err := netip.ParseAddr(normalized)
	if err == nil {
		if err := rejectUnsafeWebFetchAddr(addr); err != nil {
			return nil, err
		}
		return []netip.Addr{addr.Unmap()}, nil
	}
	if resolver == nil {
		if requireResolver {
			return nil, fmt.Errorf("host resolver is required")
		}
		return nil, nil
	}

	addrs, err := resolver.LookupNetIP(ctx, "ip", normalized)
	if err != nil {
		return nil, fmt.Errorf("resolve host: %w", err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("host did not resolve to an IP address")
	}
	for _, addr := range addrs {
		if err := rejectUnsafeWebFetchAddr(addr); err != nil {
			return nil, err
		}
	}
	return addrs, nil
}

func rejectUnsafeWebFetchAddr(addr netip.Addr) error {
	if !addr.IsValid() {
		return fmt.Errorf("invalid resolved host address")
	}
	addr = addr.Unmap()
	if embedded, ok := webFetchEmbeddedIPv4(addr); ok {
		return rejectUnsafeWebFetchAddr(embedded)
	}
	for _, blocked := range webFetchBlockedAddrPrefixes {
		if blocked.prefix.Contains(addr) {
			return errors.New(blocked.reason)
		}
	}
	if !addr.IsGlobalUnicast() {
		return fmt.Errorf("non-global hosts are blocked")
	}
	return nil
}

func webFetchEmbeddedIPv4(addr netip.Addr) (netip.Addr, bool) {
	if addr.Is4() {
		return netip.Addr{}, false
	}
	bytes := addr.As16()
	for _, candidate := range webFetchEmbeddedIPv4Prefixes {
		if !candidate.prefix.Contains(addr) {
			continue
		}
		if candidate.byteOffset < 0 || candidate.byteOffset+4 > len(bytes) {
			continue
		}
		embedded := [4]byte{
			bytes[candidate.byteOffset],
			bytes[candidate.byteOffset+1],
			bytes[candidate.byteOffset+2],
			bytes[candidate.byteOffset+3],
		}
		return netip.AddrFrom4(embedded), true
	}
	return netip.Addr{}, false
}

func redactWebFetchURL(value *url.URL) string {
	if value == nil {
		return ""
	}
	return redactWebFetchText(value.String())
}

func redactWebFetchText(value string) string {
	return redaction.RedactString(value, redaction.Options{})
}

func webFetchStatusLine(response *http.Response) string {
	if response == nil {
		return ""
	}
	if strings.TrimSpace(response.Status) != "" {
		return response.Status
	}
	return strings.TrimSpace(strconv.Itoa(response.StatusCode) + " " + http.StatusText(response.StatusCode))
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstWebFetchResolver(resolver webFetchResolver) webFetchResolver {
	if resolver != nil {
		return resolver
	}
	return defaultWebFetchResolver{}
}
