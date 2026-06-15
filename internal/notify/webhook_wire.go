package notify

import "strings"

// Webhook delivery is configured entirely from the environment. A webhook URL
// typically embeds a secret token, so sourcing it from the environment keeps it
// out of any on-disk config file. The sink is strictly opt-in: with
// EnvWebhookURL unset the wiring helper attaches nothing and the notifier
// behaves exactly as before.
const (
	// EnvWebhookURL holds the destination webhook/Slack URL. Empty disables it.
	EnvWebhookURL = "ZERO_NOTIFY_WEBHOOK_URL"
	// EnvWebhookSummary is an optional one-line run summary attached to every
	// payload (for example "nightly audit run").
	EnvWebhookSummary = "ZERO_NOTIFY_WEBHOOK_SUMMARY"
)

// MaybeAddWebhookSink attaches a webhook sink to n when a webhook URL is present
// in the environment, and is otherwise a no-op. env resolves an environment
// variable (pass os.Getenv); logf records one redacted line per failed delivery
// (pass nil to stay silent — for example a TUI that owns the screen). It is safe
// to call unconditionally: configuration alone decides whether the sink exists.
//
// The attached sink is still subject to the notifier's Mode/focus policy, so a
// webhook only delivers when notifications are enabled (for example
// `--notify both`), matching the rest of the notification surface.
func MaybeAddWebhookSink(n *Notifier, env func(string) string, logf func(format string, args ...any)) {
	if n == nil || env == nil {
		return
	}
	url := strings.TrimSpace(env(EnvWebhookURL))
	if url == "" {
		return
	}
	n.AddSink(NewWebhookSink(WebhookConfig{
		URL:     url,
		Summary: strings.TrimSpace(env(EnvWebhookSummary)),
		Logf:    logf,
	}))
}
