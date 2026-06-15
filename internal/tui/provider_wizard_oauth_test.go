package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/providercatalog"
)

// wizardModelAt builds a model whose provider wizard is at step with providerID
// selected.
func wizardModelAt(t *testing.T, providerID string, step providerWizardStep) model {
	t.Helper()
	m := mouseTestModel()
	w := m.newProviderWizard()
	idx := -1
	for i, d := range w.providers {
		if d.ID == providerID {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("provider %q not offered by the wizard", providerID)
	}
	w.selectedProvider = idx
	w.step = step
	m.providerWizard = w
	return m
}

func TestProviderWizardMethodChooserOAuthPath(t *testing.T) {
	m := mouseTestModel()
	m.providerWizard = m.newProviderWizard()
	if m.providerWizard.step != providerWizardStepMethod {
		t.Fatalf("wizard should open on the method chooser, got %v", m.providerWizard.step)
	}
	m.providerWizard.selectedMethod = 0 // "Sign in with OAuth" (default, first)
	next, _ := m.advanceProviderWizard()
	w := next.providerWizard
	if w.step != providerWizardStepProvider || !w.oauthMode {
		t.Fatalf("OAuth method should enter the provider step in oauthMode, got step=%v oauth=%v", w.step, w.oauthMode)
	}
	want := len(providercatalog.OAuthProviders()) + len(providercatalog.OAuthProxyProviders())
	if len(w.providers) != want {
		t.Fatalf("OAuth path should list OAuth + proxy providers, got %d want %d", len(w.providers), want)
	}
	for _, d := range w.providers {
		if !d.OAuth && !d.OAuthProxy {
			t.Fatalf("provider %q in the OAuth list is neither OAuth nor a proxy entry", d.ID)
		}
	}
}

func TestProviderWizardProxyEntryRoutesToBrowse(t *testing.T) {
	m := mouseTestModel()
	m.providerWizard = m.newProviderWizard()
	m.providerWizard.selectedMethod = 0
	next, _ := m.advanceProviderWizard() // → OAuth provider list
	found := false
	for i, d := range next.providerWizard.providers {
		if d.ID == "chatgpt-proxy" {
			next.providerWizard.selectedProvider = i
			found = true
			break
		}
	}
	if !found {
		t.Fatal("chatgpt-proxy proxy entry not offered in the OAuth list")
	}
	next, cmd := next.advanceProviderWizard()
	w := next.providerWizard
	if w.oauthMode {
		t.Fatal("selecting a proxy entry should leave OAuth mode")
	}
	if w.oauthPending || cmd != nil {
		t.Fatalf("proxy entry must not start an OAuth login (pending=%v cmd!=nil=%v)", w.oauthPending, cmd != nil)
	}
	if w.step != providerWizardStepEndpoint {
		t.Fatalf("proxy entry should route to the endpoint step, got %v", w.step)
	}
	if w.currentProvider().ID != "chatgpt-proxy" {
		t.Fatalf("selected provider = %q, want chatgpt-proxy", w.currentProvider().ID)
	}
	if strings.TrimSpace(w.baseURL) == "" {
		t.Fatal("proxy entry should pre-fill the default proxy URL")
	}
}

func TestProviderWizardMethodChooserBrowsePath(t *testing.T) {
	m := mouseTestModel()
	m.providerWizard = m.newProviderWizard()
	m.providerWizard.selectedMethod = len(providerWizardMethodOptions()) - 1 // "browse / API key"
	next, _ := m.advanceProviderWizard()
	w := next.providerWizard
	if w.step != providerWizardStepProvider || w.oauthMode {
		t.Fatalf("browse method should enter the provider step (not oauthMode), got step=%v oauth=%v", w.step, w.oauthMode)
	}
	if len(w.providers) <= len(providercatalog.OAuthProviders()) {
		t.Fatalf("browse path should list the full catalog, got %d", len(w.providers))
	}
}

func TestProviderWizardOAuthDispatchFromList(t *testing.T) {
	m := mouseTestModel()
	m.providerWizard = m.newProviderWizard()
	m.providerWizard.selectedMethod = 0
	next, _ := m.advanceProviderWizard() // → OAuth provider list
	// select openrouter
	for i, d := range next.providerWizard.providers {
		if d.ID == "openrouter" {
			next.providerWizard.selectedProvider = i
		}
	}
	next, cmd := next.advanceProviderWizard()
	if !next.providerWizard.oauthPending {
		t.Fatal("advancing from the OAuth list should start the login (oauthPending)")
	}
	if cmd == nil {
		t.Fatal("advancing from the OAuth list should return the OAuth command")
	}
}

func TestProviderWizardRetreatFromProviderToMethod(t *testing.T) {
	m := mouseTestModel()
	m.providerWizard = m.newProviderWizard()
	m.providerWizard.selectedMethod = 0
	next, _ := m.advanceProviderWizard() // → OAuth provider list (oauthMode)
	next.providerWizard.retreat()
	if next.providerWizard.step != providerWizardStepMethod {
		t.Fatalf("retreat from provider should return to method, got %v", next.providerWizard.step)
	}
	if next.providerWizard.oauthMode {
		t.Fatal("retreat to method should clear oauthMode")
	}
}

func TestProviderWizardSupportsOAuth(t *testing.T) {
	or, _ := providercatalog.Get("openrouter")
	if !providerWizardSupportsOAuth(or) {
		t.Fatal("openrouter should offer in-wizard OAuth")
	}
	oa, _ := providercatalog.Get("openai")
	if providerWizardSupportsOAuth(oa) {
		t.Fatal("openai should NOT offer in-wizard OAuth (no usable direct OAuth)")
	}
}

func TestProviderWizardCtrlOStartsOAuthForOpenRouter(t *testing.T) {
	m := wizardModelAt(t, "openrouter", providerWizardStepCredential)
	next, cmd := m.handleProviderWizardKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	if next.providerWizard == nil || !next.providerWizard.oauthPending {
		t.Fatal("ctrl+o should mark the wizard oauthPending")
	}
	if cmd == nil {
		t.Fatal("ctrl+o should return a command to run the OAuth flow")
	}
}

func TestProviderWizardCtrlONoopForNonOAuthProvider(t *testing.T) {
	m := wizardModelAt(t, "openai", providerWizardStepCredential)
	next, _ := m.handleProviderWizardKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	if next.providerWizard != nil && next.providerWizard.oauthPending {
		t.Fatal("ctrl+o must not start OAuth for a provider that doesn't support it")
	}
}

func TestApplyProviderWizardOAuthSuccessAdvances(t *testing.T) {
	m := wizardModelAt(t, "openrouter", providerWizardStepCredential)
	m.providerWizard.oauthPending = true
	next, _ := m.applyProviderWizardOAuth(providerWizardOAuthMsg{apiKey: "sk-or-minted"})
	if next.providerWizard == nil {
		t.Fatal("wizard should remain open")
	}
	if next.providerWizard.oauthPending {
		t.Fatal("pending should clear on success")
	}
	if next.providerWizard.apiKey != "sk-or-minted" {
		t.Fatalf("minted key not applied: %q", next.providerWizard.apiKey)
	}
	if next.providerWizard.step != providerWizardStepModel {
		t.Fatalf("should advance to the model step, got %v", next.providerWizard.step)
	}
}

func TestApplyProviderWizardOAuthErrorStays(t *testing.T) {
	m := wizardModelAt(t, "openrouter", providerWizardStepCredential)
	m.providerWizard.oauthPending = true
	next, _ := m.applyProviderWizardOAuth(providerWizardOAuthMsg{err: errors.New("nope")})
	if next.providerWizard == nil {
		t.Fatal("wizard should remain open on error")
	}
	if next.providerWizard.oauthPending {
		t.Fatal("pending should clear on error")
	}
	if next.providerWizard.step != providerWizardStepCredential {
		t.Fatalf("should stay at credential step, got %v", next.providerWizard.step)
	}
	if next.providerWizard.oauthErr == "" {
		t.Fatal("oauthErr should be set")
	}
}

func TestRenderCredentialStepShowsOAuthHintAndPending(t *testing.T) {
	m := wizardModelAt(t, "openrouter", providerWizardStepCredential)
	w := m.providerWizard
	if !strings.Contains(strings.Join(w.renderCredentialStep(80), "\n"), "ctrl+o") {
		t.Fatal("credential step should show the ctrl+o OAuth hint for openrouter")
	}
	w.oauthPending = true
	if !strings.Contains(strings.Join(w.renderOAuthWaiting(80), "\n"), "Waiting for authorization") {
		t.Fatal("pending state should show the browser-waiting message")
	}
}
