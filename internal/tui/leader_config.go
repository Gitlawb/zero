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
func resolveLeaderConfig(file config.KeybindingsFile, toggles keyBindings) resolvedLeader {
	out := resolvedLeader{
		key:      defaultLeaderKeyBinding(),
		commands: defaultLeaderCommands(),
	}

	// --- leaderKey ---
	if raw := strings.TrimSpace(file.LeaderKey); raw != "" {
		parsed := parseBinding(raw)
		switch {
		case parsed.isZero():
			out.notices = append(out.notices, fmt.Sprintf(
				"keybindings.leaderKey (%q) is invalid; using %s instead.",
				raw, config.DefaultLeaderKey))
		case !parsed.ctrl && !parsed.alt && !parsed.cmd:
			out.notices = append(out.notices, fmt.Sprintf(
				"keybindings.leaderKey (%q) must include a modifier (ctrl/alt/cmd); using %s instead.",
				raw, config.DefaultLeaderKey))
		case leaderKeyReserved(parsed):
			out.notices = append(out.notices, fmt.Sprintf(
				"keybindings.leaderKey (%s) conflicts with a reserved shortcut; using %s instead.",
				parsed.Label(), config.DefaultLeaderKey))
		case leaderKeyConflictsToggle(parsed, toggles):
			out.notices = append(out.notices, fmt.Sprintf(
				"keybindings.leaderKey (%s) conflicts with a global toggle binding; using %s instead.",
				parsed.Label(), config.DefaultLeaderKey))
		default:
			out.key = parsed
		}
	}

	// --- leader map (slash → letter) ---
	if len(file.Leader) == 0 {
		return out
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
	return out
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
