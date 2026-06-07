package zeroline

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// When a picker/suggestion overlay coexists with a permission prompt, RenderChat
// must keep the rendered button row aligned with PermLayout's hitboxes. The
// overlay is suppressed during a permission prompt (PermLayout assumes no overlay
// rows), so the buttons can't drift even when ChatData carries an overlay.
func TestPermLayoutMatchesRenderWithOverlayPresent(t *testing.T) {
	w, h := 90, 24
	g := PermLayout(w, h)
	out := RenderChat(ChatData{
		Variant: 0, Dark: true, Width: w, Height: h,
		Perm: &Perm{Tool: "edit_file", Risk: "medium", Reason: "writes a file"},
		// An overlay is also active — it must NOT shift the modal/hitboxes.
		Suggestions: []Suggestion{
			{Name: "/help", Desc: "show help"},
			{Name: "/model", Desc: "switch model"},
			{Name: "/theme", Desc: "switch theme"},
		},
		SelectedIdx: 0,
		Picker:      &Picker{Title: "pick", Items: []string{"a", "b", "c"}, Selected: 0},
	})
	lines := strings.Split(out, "\n")
	if g.Allow.Y >= len(lines) {
		t.Fatalf("allow row %d beyond frame height %d", g.Allow.Y, len(lines))
	}
	row := stripANSI(lines[g.Allow.Y])
	if !strings.Contains(row, "allow") || !strings.Contains(row, "deny") {
		t.Fatalf("button row %d does not contain the buttons: %q", g.Allow.Y, row)
	}
	// The overlay must be suppressed: the suggestion/picker content must not appear.
	full := stripANSI(out)
	if strings.Contains(full, "show help") || strings.Contains(full, "switch model") {
		t.Errorf("suggestions overlay leaked while a permission prompt was up:\n%s", full)
	}
	// The frame must still be exactly h rows tall.
	if len(lines) != h {
		t.Errorf("frame height = %d rows, want %d", len(lines), h)
	}
}

// An oversized overlay (more items than fit) must be capped so the chat frame
// stays at exactly its allotted height instead of overflowing.
func TestOverlayCappedKeepsFrameHeight(t *testing.T) {
	h := 16
	items := make([]Suggestion, 40)
	for i := range items {
		items[i] = Suggestion{Name: "/cmd", Desc: "a command"}
	}
	out := RenderChat(ChatData{
		Variant: 0, Dark: true, Width: 80, Height: h,
		Suggestions: items,
		SelectedIdx: 0,
	})
	lines := strings.Split(out, "\n")
	if len(lines) != h {
		t.Fatalf("frame height = %d rows, want %d (overlay overflowed)", len(lines), h)
	}
	// The cap must surface a "… N more" summary rather than silently dropping rows.
	if !strings.Contains(stripANSI(out), "more") {
		t.Errorf("expected a '… N more' summary for the capped overlay:\n%s", stripANSI(out))
	}
}

// A capped picker overlay (title + many items) must also keep the frame height.
func TestPickerOverlayCappedKeepsFrameHeight(t *testing.T) {
	h := 14
	items := make([]string, 50)
	for i := range items {
		items[i] = "theme-option"
	}
	out := RenderChat(ChatData{
		Variant: 0, Dark: true, Width: 80, Height: h,
		Picker: &Picker{Title: "pick a theme", Items: items, Selected: 0},
	})
	lines := strings.Split(out, "\n")
	if len(lines) != h {
		t.Fatalf("frame height = %d rows, want %d (picker overlay overflowed)", len(lines), h)
	}
}

// clip must budget by display width, not rune count: wide runes (CJK/emoji)
// occupy two cells each, so a naive rune-count clip would let the line exceed
// its width budget.
func TestClipBudgetsByDisplayWidth(t *testing.T) {
	cases := []string{
		strings.Repeat("世", 20),  // CJK, 2 cells each
		strings.Repeat("🚀", 15),  // emoji, 2 cells each
		"mix 世界 of 漢字 and ascii", // mixed
	}
	for _, w := range []int{4, 8, 12, 20} {
		for _, in := range cases {
			got := clip(in, w)
			if width := lipgloss.Width(got); width > w {
				t.Errorf("clip(%q, %d) width = %d, exceeds budget %d (got %q)", in, w, width, w, got)
			}
		}
	}
}

// clip must leave short strings untouched and return "" for a non-positive budget.
func TestClipLeavesShortStringsAndZeroWidth(t *testing.T) {
	if got := clip("hi", 10); got != "hi" {
		t.Errorf("clip kept budget but altered short string: %q", got)
	}
	if got := clip("anything", 0); got != "" {
		t.Errorf("clip(_, 0) = %q, want empty", got)
	}
}

func TestWrapBudgetsByDisplayWidth(t *testing.T) {
	// "你好"=4 display cells/6 bytes, "世界"=4 cells/6 bytes. At width 10 they fit
	// on one line by display width (4+1+4=9 <= 10) but byte-counting (6+1+6=13)
	// would wrap. wrap must use display width like clip does.
	got := wrap("你好 世界", 10)
	if len(got) != 1 || got[0] != "你好 世界" {
		t.Fatalf("wrap should keep wide-rune words on one line by display width, got %#v", got)
	}
}

func TestOverlaySuppressedDuringAskUser(t *testing.T) {
	// A pending ask_user questionnaire is the focused blocking surface; a
	// slash-command suggestion overlay must be suppressed so its rows don't consume
	// bodyH and push the question offscreen.
	d := ChatData{
		Variant: 0, Dark: true, Width: 80, Height: 24,
		AskUser:     &AskUser{Question: "Pick one?", Options: []string{"a", "b"}, Total: 1},
		Suggestions: []Suggestion{{Name: "/zzsuggest", Desc: "should be hidden"}},
	}
	out := stripANSI(RenderChat(d))
	if strings.Contains(out, "/zzsuggest") {
		t.Errorf("suggestion overlay must be suppressed while AskUser is active:\n%s", out)
	}
}

func TestPlainAssistantClipsLongUnbrokenToken(t *testing.T) {
	// A single unbroken token (long URL / CJK run with no spaces) longer than the
	// frame must NOT overflow: wrap won't break it, so the plain path must clip
	// every rendered prose line to the budget. Scoped to the prose line carrying
	// the token (the statusline footer width is a separate concern).
	const W = 120
	long := strings.Repeat("x", 400)
	out := stripANSI(RenderChat(ChatData{Variant: 0, Dark: true, Width: W, Height: 24, Stream: long}))
	sawToken := false
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "xxx") {
			sawToken = true
			if lipgloss.Width(ln) > W {
				t.Fatalf("plain prose line with a long token exceeds frame width %d (%d cells)", W, lipgloss.Width(ln))
			}
		}
	}
	if !sawToken {
		t.Fatal("expected the streamed long token to render")
	}
}
