package tui

import (
	"strings"
	"testing"
)

func TestHealthCommandAliasResolvesToDoctor(t *testing.T) {
	got := parseCommand("/health")
	if got.kind != commandDoctor {
		t.Fatalf("/health kind = %v, want %v", got.kind, commandDoctor)
	}
	if got.name != "/doctor" {
		t.Fatalf("/health canonical name = %q, want /doctor", got.name)
	}
}

func TestDoctorCommandPreservesConnectivityArgs(t *testing.T) {
	got := parseCommand("/doctor --connectivity")
	if got.kind != commandDoctor {
		t.Fatalf("/doctor --connectivity kind = %v, want %v", got.kind, commandDoctor)
	}
	if got.text != "--connectivity" {
		t.Fatalf("/doctor --connectivity text = %q, want --connectivity", got.text)
	}
}

func TestDoctorHelpListsHealthAliasAndConnectivity(t *testing.T) {
	help := strings.Join(formatCommandHelpLines(), "\n")

	for _, want := range []string{
		"/doctor [--connectivity] (/health)",
		"connectivity",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("expected command help to contain %q, got:\n%s", want, help)
		}
	}
}

func TestHealthAliasIsDiscoverableByNamesAndAutocomplete(t *testing.T) {
	if !healthCommandStringSliceContains(listCommandNames(), "/health") {
		t.Fatalf("expected command names to contain /health, got %#v", listCommandNames())
	}
	if !healthCommandSuggestionNamesContain(matchCommandSuggestions("/hea"), "/doctor") {
		t.Fatalf("expected autocomplete for /hea to surface canonical /doctor")
	}
}

func healthCommandStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func healthCommandSuggestionNamesContain(suggestions []commandSuggestion, want string) bool {
	for _, suggestion := range suggestions {
		if suggestion.Name == want {
			return true
		}
	}
	return false
}
