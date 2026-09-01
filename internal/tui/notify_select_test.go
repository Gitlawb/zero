package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/notify"
)

// A committed /notify choice is written to user config and reloaded at startup
// (via the resolver's defaults + the notifyMode/notifyFocusMode fields on the
// model), so a /notify choice survives restart, just like /theme.
func TestNotifyChoicePersistsAcrossRestart(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	// First session: pick a non-default notify pair via the text handler (same
	// commit path the picker uses via choosePicker).
	m := newModel(context.Background(), Options{UserConfigPath: cfgPath})
	m, out := m.handleNotifyCommand("off")
	if m.notifyMode != "off" {
		t.Fatalf("notifyMode = %q, want off", m.notifyMode)
	}
	if !strings.Contains(out, "Notify") {
		t.Fatalf("output should announce the change, got: %s", out)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("notify commit should have written config: %v", err)
	}
	var cfg struct {
		Notify config.NotifyConfig `json:"notify"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	if cfg.Notify.Mode != "off" {
		t.Fatalf("notify.mode = %q, want off", cfg.Notify.Mode)
	}

	// Second session: the persisted notify block seeds the model fields so the
	// /notify state line is correct and a permission prompt uses the right
	// notifier (the runtime notifier is built from options.Notify, which is
	// populated by the resolver from the same file).
	restarted := newModel(context.Background(), Options{UserConfigPath: cfgPath, Notify: config.NotifyConfig{Mode: "off"}})
	if restarted.notifyMode != "off" {
		t.Fatalf("restarted notifyMode = %q, want off (from saved config)", restarted.notifyMode)
	}
}

// `/notify` with a mode-only arg keeps the existing focusMode. A common mistake
// would be to reset the focus rule on every mode change.
func TestNotifyCommandPreservesFocusOnModeOnly(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.notifyFocusMode = string(notify.FocusAlways)
	m, _ = m.handleNotifyCommand("off")
	if m.notifyMode != "off" {
		t.Errorf("notifyMode = %q, want off", m.notifyMode)
	}
	if m.notifyFocusMode != string(notify.FocusAlways) {
		t.Errorf("notifyFocusMode = %q, want preserved %q", m.notifyFocusMode, notify.FocusAlways)
	}
}

// `/notify bell unfocused` updates both fields in one call.
func TestNotifyCommandSetsModeAndFocus(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m, _ = m.handleNotifyCommand("bell unfocused")
	if m.notifyMode != "bell" {
		t.Errorf("notifyMode = %q, want bell", m.notifyMode)
	}
	if m.notifyFocusMode != "unfocused" {
		t.Errorf("notifyFocusMode = %q, want unfocused", m.notifyFocusMode)
	}
}

// The choice reaches the LIVE notifier immediately, so the change applies on
// the next permission prompt in this session (not only after a restart).
func TestNotifyCommandAppliesToLiveNotifier(t *testing.T) {
	var buf bytes.Buffer
	// Construct through newModel so both fields are populated the way the real
	// session does; then swap in a buffer-backed notifier to observe output.
	m := newModel(context.Background(), Options{Notify: config.NotifyConfig{Mode: "off", FocusMode: "always"}})
	m.notifier = notify.New(&buf, notify.Config{Mode: notify.ModeOff, FocusMode: notify.FocusAlways})
	m.notifier.SetFocused(true)

	m, _ = m.handleNotifyCommand("bell")
	m.notifier.Notify(notify.Completion, "x")
	if buf.String() != "\x07" {
		t.Fatalf("live notifier should bell after /notify bell, got %q", buf.String())
	}

	m, _ = m.handleNotifyCommand("off")
	m.notifier.Notify(notify.Completion, "x")
	if buf.String() != "\x07" {
		t.Fatalf("live notifier should go silent after /notify off, got %q", buf.String())
	}
}

// `/notify off always typo` (more than two tokens) is rejected, not silently
// accepted as a successful change.
func TestNotifyCommandRejectsTrailingArguments(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.notifyMode = "both"
	m, out := m.handleNotifyCommand("off always typo")
	if m.notifyMode != "both" {
		t.Errorf("trailing args should not mutate state, got %q", m.notifyMode)
	}
	if !strings.Contains(out, "Too many arguments") {
		t.Errorf("output should explain the rejection, got: %s", out)
	}
}

// `/notify loud` (invalid) returns an error message; the model's notifyMode
// is NOT mutated, so a typo cannot accidentally turn the alert off.
func TestNotifyCommandRejectsInvalidMode(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.notifyMode = "both"
	m, out := m.handleNotifyCommand("loud")
	if m.notifyMode != "both" {
		t.Errorf("invalid mode should not mutate state, got %q", m.notifyMode)
	}
	if !strings.Contains(out, "Unknown mode") {
		t.Errorf("output should explain the error, got: %s", out)
	}
}

// `/notify bell sideways` rejects the focus mode but the call also failed
// validation before persisting, so neither field should change.
func TestNotifyCommandRejectsInvalidFocus(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.notifyMode = "bell"
	m.notifyFocusMode = "always"
	m, out := m.handleNotifyCommand("bell sideways")
	if m.notifyMode != "bell" || m.notifyFocusMode != "always" {
		t.Errorf("invalid focus should not mutate state, got mode=%q focus=%q", m.notifyMode, m.notifyFocusMode)
	}
	if !strings.Contains(out, "Unknown focus mode") {
		t.Errorf("output should explain the error, got: %s", out)
	}
}

// `/notify` with no argument opens the picker, just like /theme and /model.
func TestNotifyPickerOpensOnBareNotify(t *testing.T) {
	m := newModel(context.Background(), Options{Notify: config.NotifyConfig{Mode: "off", FocusMode: "unfocused"}})
	m.input.SetValue("/notify")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if cmd != nil {
		t.Fatalf("opening the notify picker should not emit a cmd, got %T", cmd)
	}
	if m.picker == nil || m.picker.kind != pickerNotify {
		t.Fatalf("expected the notify picker to open, got %#v", m.picker)
	}
	if len(m.picker.items) != len(notifyChoices) {
		t.Fatalf("picker has %d items, want %d", len(m.picker.items), len(notifyChoices))
	}
	// The preselected row should match the active (mode, focus) pair.
	sel := m.picker.items[m.picker.selected]
	if sel.Value != "off unfocused" {
		t.Errorf("preselected value = %q, want the active pair %q", sel.Value, "off unfocused")
	}
}

// The picker's Value strings are the same "<mode> <focus>" form the text
// handler accepts, so the commit path can be shared. This is the contract that
// lets choosePicker dispatch to handleNotifyCommand without translation.
func TestNotifyPickerValuesAreValidCommandArgs(t *testing.T) {
	m := newModel(context.Background(), Options{})
	picker := m.newNotifyPicker()
	for _, item := range picker.items {
		tokens := strings.Fields(item.Value)
		if len(tokens) != 2 {
			t.Errorf("item %q has %d tokens, want 2 (mode focus)", item.Value, len(tokens))
			continue
		}
		if !isValidNotifyMode(tokens[0]) {
			t.Errorf("item %q: mode %q is not a valid notify mode", item.Value, tokens[0])
		}
		if !isValidNotifyFocusMode(tokens[1]) {
			t.Errorf("item %q: focus %q is not a valid focus mode", item.Value, tokens[1])
		}
	}
}

// The /notify state view shows the current mode and focus so users can see
// the value before opening the picker.
func TestNotifyStateTextShowsActivePair(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.notifyMode = "both"
	m.notifyFocusMode = "unfocused"
	state := m.notifyStateText()
	if !strings.Contains(state, "active mode: both") {
		t.Errorf("state should show active mode, got: %s", state)
	}
	if !strings.Contains(state, "active focus: unfocused") {
		t.Errorf("state should show active focus, got: %s", state)
	}
}

// notifyCurrentMode / notifyCurrentFocusMode surface the in-session fields
// that newModel populates from options.Notify, so /notify reads the same
// value the runtime notifier uses.
func TestNotifyCurrentReflectsModelFields(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.notifyMode = "bell"
	m.notifyFocusMode = "always"
	if got := m.notifyCurrentMode(); got != "bell" {
		t.Errorf("notifyCurrentMode = %q, want bell", got)
	}
	if got := m.notifyCurrentFocusMode(); got != "always" {
		t.Errorf("notifyCurrentFocusMode = %q, want always", got)
	}
}
