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

// A committed /notify choice is written to user config and reloaded at startup,
// so a /notify choice survives restart, just like /theme.
func TestNotifyChoicePersistsAcrossRestart(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	// First session: pick a notify pair via the text handler (the same commit
	// path the picker uses via choosePicker).
	m := newModel(context.Background(), Options{UserConfigPath: cfgPath})
	m, out := m.handleNotifyCommand("off always")
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
	if cfg.Notify.Mode != "off" || cfg.Notify.FocusMode != "always" {
		t.Fatalf("notify = %+v, want mode=off focusMode=always", cfg.Notify)
	}

	// Second session: the persisted notify block seeds startup (options.Notify
	// is populated by the resolver from the same file).
	restarted := newModel(context.Background(), Options{UserConfigPath: cfgPath, Notify: config.NotifyConfig{Mode: "off", FocusMode: "always"}})
	if restarted.notifyMode != "off" || restarted.notifyFocusMode != "always" {
		t.Fatalf("restarted = mode %q focus %q, want off/always (from saved config)", restarted.notifyMode, restarted.notifyFocusMode)
	}
}

// `/notify bell` (mode-only) preserves the focusMode stored in the USER'S OWN
// file — not the resolved view. A project config's choice must not be copied
// into the user's global file, and a blank focus must stay blank (blank means
// "use the built-in default"), so nothing is pinned as an explicit choice the
// user never made. Maintainer review, PR #1001.
func TestNotifyModeOnlyPreservesStoredFocusNotResolved(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"notify":{"focusMode":"focused"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// The in-session (resolved) focus disagrees with the user's file; the
	// write must take the user's file.
	m := newModel(context.Background(), Options{
		UserConfigPath: cfgPath,
		Notify:         config.NotifyConfig{Mode: "both", FocusMode: "unfocused"},
	})
	m, _ = m.handleNotifyCommand("bell")
	persisted := readNotifyBlock(t, cfgPath)
	if persisted.Mode != "bell" {
		t.Errorf("persisted mode = %q, want bell", persisted.Mode)
	}
	if persisted.FocusMode != "focused" {
		t.Errorf("persisted focusMode = %q, want the user-file value focused (not the resolved unfocused)", persisted.FocusMode)
	}

	// Blank stays blank: with nothing stored, a mode-only change must not pin
	// the default focus as an explicit choice.
	blankPath := filepath.Join(t.TempDir(), "config.json")
	m = newModel(context.Background(), Options{UserConfigPath: blankPath})
	m, _ = m.handleNotifyCommand("off")
	persisted = readNotifyBlock(t, blankPath)
	if persisted.Mode != "off" {
		t.Errorf("persisted mode = %q, want off", persisted.Mode)
	}
	if persisted.FocusMode != "" {
		t.Errorf("persisted focusMode = %q, want blank (unspecified stays unspecified)", persisted.FocusMode)
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
	m := newModel(context.Background(), Options{Notify: config.NotifyConfig{Mode: "off", FocusMode: "always"}})
	m.notifier = notify.New(&buf, notify.Config{Mode: notify.ModeOff, FocusMode: notify.FocusAlways})
	m.notifier.SetFocused(true)

	m, _ = m.handleNotifyCommand("bell always")
	m.notifier.Notify(notify.Completion, "x")
	if buf.String() != "\x07" {
		t.Fatalf("live notifier should bell after /notify bell, got %q", buf.String())
	}

	m, _ = m.handleNotifyCommand("off always")
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

// `/notify bell sideways` fails validation before persisting, so neither field
// changes.
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

// The picker enumerates the FULL mode x focus space (12 rows), so every valid
// pair is representable and Enter can never silently commit a different pair
// than the user's current one. Maintainer review, PR #1001: with a 4-row
// curated list, opening the picker on (off, always) preselected row 0
// (both, unfocused) and Enter changed the setting.
func TestNotifyPickerEnumeratesFullSpace(t *testing.T) {
	m := newModel(context.Background(), Options{})
	picker := m.newNotifyPicker()
	if len(picker.items) != 12 {
		t.Fatalf("picker has %d items, want 12 (4 modes x 3 focus modes)", len(picker.items))
	}
	seen := map[string]bool{}
	for _, item := range picker.items {
		if seen[item.Value] {
			t.Errorf("duplicate picker row %q", item.Value)
		}
		seen[item.Value] = true
	}
	for _, mode := range []string{"off", "bell", "notify", "both"} {
		for _, focus := range []string{"unfocused", "always", "focused"} {
			if !seen[mode+" "+focus] {
				t.Errorf("picker is missing row for valid pair %q %q", mode, focus)
			}
		}
	}
}

// Every pair the picker preselects must also be a row: Enter on an open
// picker must keep (or explicitly change) the user's actual setting. This is
// the maintainer's suggested regression: send Enter to an open picker from a
// pair that is NOT in a 4-row curated list — with full enumeration the
// preselected row IS the current pair and the commit is a no-op change.
func TestNotifyPickerEnterOnUnlistedPairKeepsSetting(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"notify":{"mode":"off","focusMode":"always"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), Options{
		UserConfigPath: cfgPath,
		Notify:         config.NotifyConfig{Mode: "off", FocusMode: "always"},
	})
	m.input.SetValue("/notify")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if cmd != nil {
		t.Fatalf("opening the notify picker should not emit a cmd, got %T", cmd)
	}
	if m.picker == nil || m.picker.kind != pickerNotify {
		t.Fatalf("expected the notify picker to open, got %#v", m.picker)
	}
	// (off, always) must be preselected — it is a valid pair even though the
	// old curated list could not represent it.
	if sel := m.picker.items[m.picker.selected]; sel.Value != "off always" {
		t.Fatalf("preselected = %q, want the current pair %q", sel.Value, "off always")
	}

	// Enter commits the preselected row: the setting is unchanged.
	updated, _ = m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.picker != nil {
		t.Fatal("picker should close on Enter")
	}
	persisted := readNotifyBlock(t, cfgPath)
	if persisted.Mode != "off" || persisted.FocusMode != "always" {
		t.Fatalf("Enter changed the setting: got %+v, want off/always unchanged", persisted)
	}
}

// The picker's Value strings are the same "<mode> <focus>" form the text
// handler accepts, so the commit path can be shared. This is the contract that
// lets choosePicker dispatch to handleNotifyCommand without translation.
func TestNotifyPickerValuesAreValidCommandArgs(t *testing.T) {
	m := newModel(context.Background(), Options{})
	picker := m.newNotifyPicker()
	if len(picker.items) == 0 {
		t.Fatal("picker has no items")
	}
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

// The /notify state view shows the stored mode and focus (and labels a blank
// focus as the default) so users see the real value before opening the picker.
func TestNotifyStateTextShowsStoredPair(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"notify":{"mode":"bell","focusMode":"always"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), Options{UserConfigPath: cfgPath})
	state := m.notifyStateText()
	if !strings.Contains(state, "active mode: bell") {
		t.Errorf("state should show stored mode, got: %s", state)
	}
	if !strings.Contains(state, "active focus: always") {
		t.Errorf("state should show stored focus, got: %s", state)
	}
}

func readNotifyBlock(t *testing.T, path string) config.NotifyConfig {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config %s: %v", path, err)
	}
	var cfg struct {
		Notify config.NotifyConfig `json:"notify"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode config %s: %v", path, err)
	}
	return cfg.Notify
}
