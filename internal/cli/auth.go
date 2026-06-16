package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/oauth"
	"github.com/Gitlawb/zero/internal/redaction"
)

// runAuth dispatches `zero auth <command>` for provider OAuth login. It is
// additive and independent of `zero mcp oauth` (MCP server auth), which is
// unchanged.
func runAuth(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 {
		return writeExecUsageError(stderr, "auth subcommand required. Use `zero auth status`.")
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		if err := writeAuthHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	switch args[0] {
	case "login":
		return runAuthLogin(args[1:], stdout, stderr, deps)
	case "logout":
		return runAuthLogout(args[1:], stdout, stderr, deps)
	case "status":
		return runAuthStatus(args[1:], stdout, stderr, deps)
	case "refresh":
		return runAuthRefresh(args[1:], stdout, stderr, deps)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown auth subcommand %q", args[0]))
	}
}

// authArgs is the parsed form of an auth subcommand's arguments.
type authArgs struct {
	positional []string
	json       bool
	device     bool
	watch      bool
	scopes     []string
	help       bool
}

func parseAuthArgs(sub string, args []string) (authArgs, error) {
	var parsed authArgs
	addScope := func(scope string) error {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			return fmt.Errorf("--scope requires a non-empty value")
		}
		parsed.scopes = append(parsed.scopes, scope)
		return nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			parsed.help = true
		case arg == "--json":
			parsed.json = true
		case arg == "--device":
			parsed.device = true
		case arg == "--watch":
			parsed.watch = true
		case arg == "--scope":
			if i+1 >= len(args) {
				return authArgs{}, fmt.Errorf("--scope requires a value")
			}
			i++
			if err := addScope(args[i]); err != nil {
				return authArgs{}, err
			}
		case strings.HasPrefix(arg, "--scope="):
			if err := addScope(strings.TrimPrefix(arg, "--scope=")); err != nil {
				return authArgs{}, err
			}
		case strings.HasPrefix(arg, "-"):
			return authArgs{}, fmt.Errorf("unknown flag %q", arg)
		default:
			parsed.positional = append(parsed.positional, arg)
		}
	}
	if parsed.help {
		return parsed, nil // help short-circuits flag validation
	}
	if err := validateAuthFlags(sub, parsed); err != nil {
		return authArgs{}, err
	}
	return parsed, nil
}

// validateAuthFlags rejects flags a subcommand does not accept, so an ambiguous
// invocation fails fast instead of silently ignoring a flag.
func validateAuthFlags(sub string, a authArgs) error {
	allowed := map[string]map[string]bool{
		"login":   {"device": true, "scope": true},
		"logout":  {"json": true},
		"status":  {"json": true},
		"refresh": {"watch": true},
	}[sub]
	bad := func(name string) error { return fmt.Errorf("zero auth %s does not accept %s", sub, name) }
	if a.json && !allowed["json"] {
		return bad("--json")
	}
	if a.device && !allowed["device"] {
		return bad("--device")
	}
	if a.watch && !allowed["watch"] {
		return bad("--watch")
	}
	if len(a.scopes) > 0 && !allowed["scope"] {
		return bad("--scope")
	}
	return nil
}

// newAuthManager builds an oauth.Manager backed by the file store, printing the
// authorization URL / device code to stdout. The store path honors
// ZERO_OAUTH_TOKENS_PATH (env), so callers/tests can redirect it. Setting
// ZERO_OAUTH_STORAGE=encrypted-file selects the AES-256-GCM encrypted-at-rest
// backend (a per-user secret is created beside the token file).
func newAuthManager(deps appDeps, out io.Writer) (*oauth.Manager, error) {
	// Validate ZERO_OAUTH_STORAGE up front: a mistyped value must fail fast rather
	// than silently change the backend. Empty = default (plaintext 0600 file);
	// "encrypted-file" = AES-256-GCM; "keyring" = the OS keyring.
	storage := strings.ToLower(strings.TrimSpace(os.Getenv("ZERO_OAUTH_STORAGE")))
	switch storage {
	case "", "file", "encrypted-file", "keyring":
	default:
		return nil, fmt.Errorf("invalid ZERO_OAUTH_STORAGE %q (supported: file, encrypted-file, keyring)", storage)
	}
	store, err := oauth.NewStore(oauth.StoreOptions{
		Now:     deps.now,
		Storage: storage,
	})
	if err != nil {
		return nil, err
	}
	return oauth.NewManager(oauth.ManagerOptions{
		Store:      store,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Now:        deps.now,
		Out:        out,
		// The opener prints the URL so headless shells can copy it; the URL
		// carries no token material. A real browser launch is intentionally not
		// performed (the sandbox/headless contexts make printing the safer default).
		OpenBrowser: func(string) error { return nil },
	})
}

func runAuthLogin(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	parsed, err := parseAuthArgs("login", args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if parsed.help {
		_ = writeAuthHelp(stdout)
		return exitSuccess
	}
	if len(parsed.positional) != 1 {
		return writeExecUsageError(stderr, "usage: zero auth login <provider> [--device] [--scope <scope>]")
	}
	manager, err := newAuthManager(deps, stdout)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	status, err := manager.Login(context.Background(), oauth.LoginOptions{
		Provider:    parsed.positional[0],
		Device:      parsed.device,
		ExtraScopes: parsed.scopes,
	})
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if _, err := fmt.Fprintf(stdout, "Logged in to %s.\n%s\n", parsed.positional[0], oauth.FormatStatuses([]oauth.Status{status})); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runAuthLogout(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	parsed, err := parseAuthArgs("logout", args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if parsed.help {
		_ = writeAuthHelp(stdout)
		return exitSuccess
	}
	if len(parsed.positional) != 1 {
		return writeExecUsageError(stderr, "usage: zero auth logout <provider>")
	}
	provider := parsed.positional[0]
	manager, err := newAuthManager(deps, stdout)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	removed, err := manager.Logout(provider)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if parsed.json {
		payload := struct {
			Provider string `json:"provider"`
			Removed  bool   `json:"removed"`
		}{Provider: provider, Removed: removed}
		if err := writePrettyJSON(stdout, payload); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	msg := fmt.Sprintf("No OAuth login stored for %s.\n", provider)
	if removed {
		msg = fmt.Sprintf("Logged out of %s.\n", provider)
	}
	if _, err := fmt.Fprint(stdout, msg); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runAuthStatus(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	parsed, err := parseAuthArgs("status", args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if parsed.help {
		_ = writeAuthHelp(stdout)
		return exitSuccess
	}
	if len(parsed.positional) > 1 {
		return writeExecUsageError(stderr, "usage: zero auth status [provider]")
	}
	manager, err := newAuthManager(deps, stdout)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	statuses, err := manager.StatusAll()
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if len(parsed.positional) == 1 {
		statuses = filterAuthStatuses(statuses, parsed.positional[0])
	}
	if parsed.json {
		payload := struct {
			Logins []oauth.Status `json:"logins"`
		}{Logins: statuses}
		if err := writePrettyJSON(stdout, payload); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, oauth.FormatStatuses(statuses)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runAuthRefresh(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	parsed, err := parseAuthArgs("refresh", args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if parsed.help {
		_ = writeAuthHelp(stdout)
		return exitSuccess
	}
	if len(parsed.positional) != 1 {
		return writeExecUsageError(stderr, "usage: zero auth refresh <provider>")
	}
	provider := parsed.positional[0]
	manager, err := newAuthManager(deps, stdout)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	key := oauth.ProviderKey(provider)
	if parsed.watch {
		return runAuthRefreshWatch(manager, key, provider, stdout, stderr)
	}
	if _, err := manager.Handle401(context.Background(), key); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if _, err := fmt.Fprintf(stdout, "Refreshed OAuth token for %s.\n", provider); err != nil {
		return exitCrash
	}
	return exitSuccess
}

// runAuthRefreshWatch keeps a provider's token fresh in the foreground until
// interrupted. This is the opt-in proactive-refresh scheduler surface (for a
// long-running external process that reads the token file). It validates a
// refreshable token exists first, then schedules refreshes before each expiry.
func runAuthRefreshWatch(manager *oauth.Manager, key, provider string, stdout io.Writer, stderr io.Writer) int {
	ctx, stop := signalContext()
	defer stop()
	// Validate (and refresh-if-needed) once up front so a missing/non-refreshable
	// token fails fast instead of silently watching nothing.
	if _, err := manager.GetFresh(ctx, key); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	scheduler := oauth.NewRefreshScheduler()
	scheduler.Start(ctx, manager, key)
	defer scheduler.Stop()
	if _, err := fmt.Fprintf(stdout, "Watching %s — refreshing before expiry. Press Ctrl+C to stop.\n", provider); err != nil {
		return exitCrash
	}
	<-ctx.Done()
	return exitSuccess
}

func filterAuthStatuses(statuses []oauth.Status, provider string) []oauth.Status {
	want := oauth.ProviderKey(provider)
	filtered := make([]oauth.Status, 0, 1)
	for _, st := range statuses {
		if st.Key == want {
			filtered = append(filtered, st)
		}
	}
	return filtered
}

func writeAuthHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero auth <command>

Commands:
  login <provider> [--device] [--scope <scope>]   Log in to a provider via OAuth
  logout <provider>                               Delete a provider's stored login
  status [provider]                               Show login presence/expiry (never the token)
  refresh <provider> [--watch]                    Force a token refresh (--watch keeps it fresh)

A provider is any OAuth 2.0 / OIDC server you configure via env (no providers are
built in). For a provider named <name>, set:
  ZERO_OAUTH_<NAME>_CLIENT_ID       (required)
  ZERO_OAUTH_<NAME>_CLIENT_SECRET   (optional)
  ZERO_OAUTH_<NAME>_AUTHORIZE_URL   ZERO_OAUTH_<NAME>_TOKEN_URL
  ZERO_OAUTH_<NAME>_DEVICE_URL      ZERO_OAUTH_<NAME>_ISSUER_URL (for discovery)
  ZERO_OAUTH_<NAME>_SCOPES          ZERO_OAUTH_<NAME>_FLOW (loopback|device)
Endpoint URLs must be https (loopback exempt).

Storage: tokens are written 0600 under $XDG_CONFIG_HOME/zero (override with
ZERO_OAUTH_TOKENS_PATH). Set ZERO_OAUTH_STORAGE=encrypted-file to encrypt them at
rest with AES-256-GCM (a per-user secret beside the file), or
ZERO_OAUTH_STORAGE=keyring to use the OS keyring (macOS Keychain / Linux
secret-tool). MCP server tokens share the same store.

Flags:
      --device   Use the device-code flow (headless/SSH; no browser)
      --scope    Add an OAuth scope (repeatable)
      --watch    Keep the token fresh in the foreground (refresh only)
      --json     Print result as JSON (status/logout)
  -h, --help     Show this help
`)
	return err
}
