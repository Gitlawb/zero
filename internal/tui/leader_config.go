package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Gitlawb/zero/internal/config"
)

// resolvedLeader is the runtime leader prefix + inverted letter→slash map.
type resolvedLeader struct {
	key      parsedBinding
	commands map[rune]string
	notices  []string
}

// defaultLeaderKeyBinding is the built-in Ctrl+X leader.
func defaultLeaderKeyBinding() parsedBinding {
	return parseBinding(config.DefaultLeaderKey)
}

// resolveLeaderConfig validates and inverts a merged KeybindingsFile into
// dispatch-ready fields. Soft-fails to defaults with notices.
// toggles may be sanitized (conflicting bindings cleared to zero) so the chosen
// leader key never shadows a global toggle; the returned keyBindings is what
// the model should use for dispatch.
func resolveLeaderConfig(file config.KeybindingsFile, toggles keyBindings) (resolvedLeader, keyBindings) {
	out := resolvedLeader{
		key:      defaultLeaderKeyBinding(),
		commands: defaultLeaderCommands(),
	}

	// --- leaderKey ---
	var preferred parsedBinding
	var rejectReason string
	if raw := strings.TrimSpace(file.LeaderKey); raw != "" {
		parsed := parseBinding(raw)
		switch {
		case parsed.isZero():
			rejectReason = fmt.Sprintf("keybindings.leaderKey (%q) is invalid", raw)
		case !leaderKeyHasModifier(parsed):
			rejectReason = fmt.Sprintf("keybindings.leaderKey (%q) must include a modifier (ctrl/alt/cmd)", raw)
		case leaderKeyReserved(parsed):
			rejectReason = fmt.Sprintf("keybindings.leaderKey (%s) conflicts with a reserved shortcut", parsed.Label())
		case leaderKeyConflictsToggle(parsed, toggles):
			rejectReason = fmt.Sprintf("keybindings.leaderKey (%s) conflicts with a global toggle binding", parsed.Label())
		default:
			preferred = parsed
		}
	}
	if !preferred.isZero() {
		out.key = preferred
	}

	// Ensure the chosen key is usable even when the default itself collides with
	// a remapped toggle (or when the preferred key was rejected and fallback
	// would still be unsafe).
	var ensureNotices []string
	out.key, toggles, ensureNotices = ensureUsableLeaderKey(out.key, toggles)
	if rejectReason != "" {
		out.notices = append(out.notices, fmt.Sprintf(
			"%s; using %s instead.", rejectReason, out.key.Label()))
	}
	out.notices = append(out.notices, ensureNotices...)

	// --- leader map (slash → letter) ---
	if len(file.Leader) == 0 {
		return out, toggles
	}

	// Start from defaults, overlay file entries.
	slashToLetter := defaultLeaderSlashToLetter()
	for slash, letter := range file.Leader {
		slash = strings.TrimSpace(slash)
		if slash == "" {
			continue
		}
		if !strings.HasPrefix(slash, "/") || strings.ContainsAny(slash, " \t") {
			out.notices = append(out.notices, fmt.Sprintf(
				"keybindings.leader %q ignored: must be a bare slash command.", slash))
			continue
		}
		letter = strings.TrimSpace(letter)
		if letter == "" {
			delete(slashToLetter, slash)
			continue
		}
		if strings.EqualFold(slash, "/edit") {
			out.notices = append(out.notices, "keybindings.leader /edit is not allowed (replaces the composer draft); ignored.")
			continue
		}
		if _, ok := resolveCommand(slash); !ok {
			out.notices = append(out.notices, fmt.Sprintf(
				"keybindings.leader %q ignored: unknown slash command.", slash))
			continue
		}
		if utf8.RuneCountInString(letter) != 1 {
			out.notices = append(out.notices, fmt.Sprintf(
				"keybindings.leader %q ignored: letter must be one character or \"\".", slash))
			continue
		}
		r := []rune(letter)[0]
		if r == '?' {
			out.notices = append(out.notices, fmt.Sprintf(
				"keybindings.leader %q ignored: '?' is reserved for the chord-map overlay.", slash))
			continue
		}
		slashToLetter[slash] = r
	}

	// Invert with stable slash order so duplicate letters drop deterministically.
	slashes := make([]string, 0, len(slashToLetter))
	for slash := range slashToLetter {
		slashes = append(slashes, slash)
	}
	sort.Strings(slashes)

	commands := make(map[rune]string, len(slashes))
	claimed := map[rune]string{}
	for _, slash := range slashes {
		r := slashToLetter[slash]
		if prev, ok := claimed[r]; ok {
			out.notices = append(out.notices, fmt.Sprintf(
				"keybindings.leader %q and %q both use letter %q; keeping %q.",
				prev, slash, string(r), prev))
			continue
		}
		claimed[r] = slash
		commands[r] = slash
	}
	out.commands = commands
	return out, toggles
}

// leaderKeyFallbacks are tried (after the built-in default) when the preferred
// leader key and the default are both unusable even after clearing colliding
// toggles. All include a modifier and avoid reservedBindings.
var leaderKeyFallbacks = []string{
	config.DefaultLeaderKey, // ctrl+x
	"alt+x",
	"ctrl+\\",
	"alt+space",
}

// ensureUsableLeaderKey returns a leader binding that is not reserved and does
// not collide with any effective global toggle. When the only way to free the
// built-in default is to drop a remapped toggle that claimed it, that toggle is
// cleared (zero → its own default) with a notice.
func ensureUsableLeaderKey(preferred parsedBinding, toggles keyBindings) (parsedBinding, keyBindings, []string) {
	var notices []string
	if leaderKeyUsable(preferred, toggles) {
		return preferred, toggles, nil
	}

	def := defaultLeaderKeyBinding()
	if leaderKeyUsable(def, toggles) {
		return def, toggles, nil
	}

	// Default is claimed by a toggle (or somehow reserved). Clear toggles that
	// use the default chord so Ctrl+X can remain the stable product default.
	if cleared, names := clearTogglesMatching(toggles, def); len(names) > 0 {
		toggles = cleared
		notices = append(notices, fmt.Sprintf(
			"keybindings leader default (%s) conflicted with %s; those toggles were reset to their defaults so the leader key stays available.",
			def.Label(), strings.Join(names, ", ")))
		if leaderKeyUsable(def, toggles) {
			return def, toggles, notices
		}
	}

	// Last resort: walk a short list of safe alternatives.
	for _, raw := range leaderKeyFallbacks {
		cand := parseBinding(raw)
		if leaderKeyUsable(cand, toggles) {
			notices = append(notices, fmt.Sprintf(
				"keybindings.leaderKey fell back to %s (no non-conflicting default was available).",
				cand.Label()))
			return cand, toggles, notices
		}
	}

	// Should be unreachable: still return default for dispatch stability.
	notices = append(notices, fmt.Sprintf(
		"keybindings.leaderKey could not find a free chord; using %s (may conflict).",
		def.Label()))
	return def, toggles, notices
}

func leaderKeyUsable(p parsedBinding, toggles keyBindings) bool {
	if p.isZero() || !leaderKeyHasModifier(p) {
		return false
	}
	if leaderKeyReserved(p) || leaderKeyConflictsToggle(p, toggles) {
		return false
	}
	return true
}

func leaderKeyHasModifier(p parsedBinding) bool {
	return p.ctrl || p.alt || p.cmd
}

// clearTogglesMatching sets any effective toggle equal to chord back to zero
// (built-in default). Returns the updated bindings and the JSON field names cleared.
func clearTogglesMatching(toggles keyBindings, chord parsedBinding) (keyBindings, []string) {
	if chord.isZero() {
		return toggles, nil
	}
	type entry struct {
		name    string
		binding *parsedBinding
		def     parsedBinding
	}
	entries := []entry{
		{"toggleDetailed", &toggles.toggleDetailed, parseBinding("ctrl+o")},
		{"toggleMouse", &toggles.toggleMouse, parseBinding("ctrl+e")},
		{"cycleReasoning", &toggles.cycleReasoning, parseBinding("ctrl+t")},
		{"togglePlan", &toggles.togglePlan, parseBinding("ctrl+y")},
		{"toggleSidebar", &toggles.toggleSidebar, parseBinding("ctrl+b")},
	}
	var names []string
	for _, e := range entries {
		if effectiveBinding(*e.binding, e.def) == chord {
			// Only clear when the effective chord is this one. If configured is
			// zero and default matches, clearing is a no-op for storage but the
			// default still matches — that case is a product conflict with a
			// built-in toggle default (e.g. leader wanting ctrl+o). Skip zeroing
			// defaults; caller should pick another leader key.
			if e.binding.isZero() {
				continue
			}
			*e.binding = parsedBinding{}
			names = append(names, "keybindings."+e.name)
		}
	}
	return toggles, names
}

func defaultLeaderCommands() map[rune]string {
	out := make(map[rune]string, len(config.DefaultLeaderAssignments()))
	for slash, letter := range config.DefaultLeaderAssignments() {
		if letter == "" {
			continue
		}
		out[[]rune(letter)[0]] = slash
	}
	return out
}

func defaultLeaderSlashToLetter() map[string]rune {
	out := make(map[string]rune, len(config.DefaultLeaderAssignments()))
	for slash, letter := range config.DefaultLeaderAssignments() {
		if letter == "" {
			continue
		}
		out[slash] = []rune(letter)[0]
	}
	return out
}

func leaderKeyReserved(p parsedBinding) bool {
	for _, reserved := range reservedBindings {
		if p == reserved.binding {
			return true
		}
	}
	return false
}

func leaderKeyConflictsToggle(p parsedBinding, toggles keyBindings) bool {
	// Effective chord for each toggle: configured value or built-in default.
	effective := []parsedBinding{
		effectiveBinding(toggles.toggleDetailed, parseBinding("ctrl+o")),
		effectiveBinding(toggles.toggleMouse, parseBinding("ctrl+e")),
		effectiveBinding(toggles.cycleReasoning, parseBinding("ctrl+t")),
		effectiveBinding(toggles.togglePlan, parseBinding("ctrl+y")),
		effectiveBinding(toggles.toggleSidebar, parseBinding("ctrl+b")),
	}
	for _, e := range effective {
		if !e.isZero() && e == p {
			return true
		}
	}
	return false
}

func effectiveBinding(configured, defaultB parsedBinding) parsedBinding {
	if !configured.isZero() {
		return configured
	}
	return defaultB
}

// leaderKeyLabel returns the display label for the resolved leader (fallback Ctrl+X).
func (m model) leaderKeyLabel() string {
	if m.leaderKey.isZero() {
		return defaultLeaderKeyBinding().Label()
	}
	return m.leaderKey.Label()
}
