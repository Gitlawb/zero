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
	if !strings.Contains(strings.Join(w.renderCredentialStep(80), "\n"), "Opening your browser") {
		t.Fatal("pending state should show the browser-waiting message")
	}
}
