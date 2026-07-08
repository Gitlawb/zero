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
	zeroSandbox "github.com/Gitlawb/zero/internal/sandbox"
)

const (
	// Raw-body budgets. HTML responses are converted to markdown before they
	// reach the model (see web_fetch_markdown.go), so a generous raw budget
	// does not translate into a generous context cost — conversion typically
	// shrinks a page by an order of magnitude. 64KiB of raw HTML often held
	// nothing but a page's <head>, which starved research tasks.
	defaultWebFetchMaxBytes = 256 * 1024
	maxWebFetchMaxBytes     = 2 * 1024 * 1024
	webFetchTimeout         = 30 * time.Second
	webFetchRedirectLimit   = 5
	webFetchErrorBodyLimit  = 4 * 1024
	webFetchPublicOnlyHint  = "web_fetch only supports public remote HTTP/HTTPS URLs. For localhost or private network URLs, use bash with curl so sandbox network permission can apply"
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
	prefix    netip.Prefix
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
			description: "Fetch text from a public remote HTTP or HTTPS URL after network permission is granted. Do not use for localhost, private network URLs, or local dev servers; use bash with curl for those.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"url": {
						Type:        "string",
						Description: "Public remote HTTP or HTTPS URL to fetch. Use bash with curl for localhost or private network URLs.",
					},
					"max_bytes": {
						Type:        "integer",
						Description: "Maximum raw response body bytes to download before conversion.",
						Default:     defaultWebFetchMaxBytes,
						Minimum:     intPtr(1),
						Maximum:     intPtr(maxWebFetchMaxBytes),
					},
					"format": {
						Type:        "string",
						Description: "auto (default): HTML responses are converted to compact markdown, everything else is returned as-is. raw: never convert. markdown: force conversion.",
						Enum:        []string{"auto", "raw", "markdown"},
						Default:     "auto",
					},
				},
				Required: []string{"url"},
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
	return tool.run(ctx, args)
}

func (tool webFetchTool) RejectBeforePermission(args map[string]any) (Result, bool) {
	rawURL, err := stringArg(args, "url", "", true)
	if err != nil {
		return errorResult("Error: Invalid arguments for web_fetch: " + err.Error()), true
	}
	if _, err := intArg(args, "max_bytes", defaultWebFetchMaxBytes, 1, maxWebFetchMaxBytes); err != nil {
		return errorResult("Error: Invalid arguments for web_fetch: " + err.Error()), true
	}
	if err := validateWebFetchURLBeforePermission(rawURL); err != nil {
		return errorResult("Error: Unsafe URL for web_fetch: " + err.Error()), true
	}
	return Result{}, false
}

// RunWithSandbox follows the normal web_fetch path. The sandbox network policy
// gates sandboxed shell egress; web_fetch is an in-process tool guarded by the
// permission flow plus URL, redirect, host, and port safety checks.
func (tool webFetchTool) RunWithSandbox(ctx context.Context, args map[string]any, _ *zeroSandbox.Engine) Result {
	return tool.run(ctx, args)
}

func (tool webFetchTool) run(ctx context.Context, args map[string]any) Result {
	rawURL, err := stringArg(args, "url", "", true)
	if err != nil {
		return errorResult("Error: Invalid arguments for web_fetch: " + err.Error())
	}
	maxBytes, err := intArg(args, "max_bytes", defaultWebFetchMaxBytes, 1, maxWebFetchMaxBytes)
	if err != nil {
		return errorResult("Error: Invalid arguments for web_fetch: " + err.Error())
	}
	format, err := stringArg(args, "format", "auto", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for web_fetch: " + err.Error())
	}
	format = strings.ToLower(strings.TrimSpace(format))
	switch format {
	case "", "auto":
		format = "auto"
	case "raw", "markdown":
	default:
		return errorResult(`Error: Invalid arguments for web_fetch: format must be "auto", "raw", or "markdown".`)
	}

	if err := validateWebFetchURLBeforePermission(rawURL); err != nil {
		return errorResult("Error: Unsafe URL for web_fetch: " + err.Error())
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

	// HTML responses are converted to compact markdown by default: raw HTML is
	// mostly markup, so conversion typically shrinks the page by an order of
	// magnitude and the model reads content instead of boilerplate. format=raw
	// is the escape hatch for pages the converter mangles.
	converted := false
	if format == "markdown" || (format == "auto" && looksLikeHTML(contentType, body)) {
		if markdown := htmlToMarkdown(body); markdown != "" {
			body = markdown
			converted = true
		}
	}

	headers := []string{
		"URL: " + finalURL,
		"Status: " + webFetchStatusLine(response),
		"Content-Type: " + firstNonEmptyString(contentType, "unknown"),
		"Bytes: " + strconv.Itoa(len(body)),
	}
	if converted {
		headers = append(headers, `Converted: html -> markdown (pass format: "raw" for the original HTML)`)
	}
	output := strings.Join(append(headers, "", body), "\n")

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
			"converted":    strconv.FormatBool(converted),
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
	transport.Proxy = http.ProxyFromEnvironment
	transport.DialContext = webFetchSafeDialContext(resolver, dialer)
	transport.DialTLSContext = nil
	return transport
}

func webFetchSafeDialContext(resolver webFetchResolver, dialer webFetchDialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network string, address string) (net.Conn, error) {
		// ponytail: forward proxy on loopback — this is the proxy address, not the endpoint
		if proxyURL := http.ProxyFromEnvironment(&http.Request{URL: &url.URL{Host: "x"}}); proxyURL != nil {
			if h, p, err := net.SplitHostPort(address); err == nil {
				if proxyURL.Hostname() == h && (proxyURL.Port() == "" || proxyURL.Port() == p) {
					return dialer.DialContext(ctx, network, address)
				}
			}
		}
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
			return fmt.Errorf("unsafe redirect URL: %w", err)
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
