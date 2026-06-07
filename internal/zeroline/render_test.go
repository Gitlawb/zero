package zeroline

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestThemesCount(t *testing.T) {
	if len(Themes) != 5 {
		t.Fatalf("expected 5 themes, got %d", len(Themes))
	}
	for i, th := range Themes {
		if th.Name == "" || th.Dark.Bg == "" || th.Light.Bg == "" {
			t.Errorf("theme %d (%q) incomplete", i, th.Name)
		}
	}
}

func TestRenderHomeAllThemes(t *testing.T) {
	for v := 0; v < len(Themes); v++ {
		out := RenderHome(HomeData{
			Variant: v, Dark: true, Width: 100, Height: 28,
			Header: Header{Cwd: "~/src/zero", Branch: "main", Model: "claude-sonnet-4.5", Provider: "anthropic"},
			Input:  "❯ message zero",
		})
		if !strings.Contains(out, "Own your agent") {
			t.Errorf("theme %d: home missing tagline", v)
		}
	}
}

func TestRenderChatLiveData(t *testing.T) {
	d := ChatData{
		Variant: 1, Dark: true, Width: 100, Height: 30,
		Header: Header{Cwd: "~/src/zero", Branch: "main", Model: "claude-sonnet-4.5", Provider: "anthropic"},
		Rows: []Row{
			{Kind: "user", Text: "refactor the loop"},
			{Kind: "toolcall", Tool: "grep", Detail: "pattern: case"},
			{Kind: "toolresult", Tool: "grep", Status: "ok", Detail: "3 matches"},
			{Kind: "assistant", Text: "Here is the plan."},
		},
	}
	out := RenderChat(d)
	for _, want := range []string{"DONE", "you", "grep", "✦ zero", "claude-sonnet-4.5"} {
		if !strings.Contains(out, want) {
			t.Errorf("chat render missing %q", want)
		}
	}

	// thinking state shows the WORKING mode + an animated thinking line
	d.Rows = d.Rows[:1]
	d.Working = true
	d.Thinking = true
	if w := RenderChat(d); !strings.Contains(w, "WORKING") || !strings.Contains(w, "thinking") {
		t.Error("thinking state not rendered")
	}
	// streaming state shows the live assistant text
	d.Thinking = false
	d.Stream = "streaming-response-here"
	if w := RenderChat(d); !strings.Contains(w, "streaming-response-here") {
		t.Error("streaming text not rendered")
	}

	// permission modal shows the gate choices and BLOCKED mode
	d.Working = false
	d.Perm = &Perm{Tool: "edit_file", Risk: "medium", Reason: "writes a file"}
	p := RenderChat(d)
	for _, want := range []string{"BLOCKED", "permission required", "edit_file", "allow", "always", "deny"} {
		if !strings.Contains(p, want) {
			t.Errorf("permission modal missing %q", want)
		}
	}
}

func TestRenderChatAskUserShowsQuestionNotSpinner(t *testing.T) {
	d := ChatData{
		Variant: 0, Dark: true, Width: 100, Height: 30,
		Header: Header{Cwd: "~/src/zero", Model: "claude-sonnet-4.5", Provider: "anthropic"},
		Rows:   []Row{{Kind: "user", Text: "scaffold a project"}},
		// A pending ask_user prompt: even though the run is technically working,
		// the questionnaire must show, not the spinner.
		Working:  true,
		Thinking: true,
		AskUser: &AskUser{
			Header:   "A couple of details",
			Question: "Which framework?",
			Options:  []string{"React", "Vue"},
			Index:    0,
			Total:    2,
			Input:    "Re",
		},
	}
	out := stripANSI(RenderChat(d))
	for _, want := range []string{"Which framework?", "React", "Vue", "question 1 of 2", "A couple of details"} {
		if !strings.Contains(out, want) {
			t.Errorf("ask_user chat render missing %q", want)
		}
	}
	if strings.Contains(out, "thinking") {
		t.Error("ask_user prompt must suppress the thinking spinner")
	}
}

func TestPermLayoutMatchesRender(t *testing.T) {
	// The buttons row in the rendered modal must sit exactly where PermLayout
	// says, so mouse clicks land on the right choice.
	w, h := 90, 24
	g := PermLayout(w, h)
	out := RenderChat(ChatData{
		Variant: 0, Dark: true, Width: w, Height: h,
		Perm: &Perm{Tool: "edit_file", Risk: "medium", Reason: "writes a file"},
	})
	lines := strings.Split(out, "\n")
	if g.Allow.Y >= len(lines) {
		t.Fatalf("allow row %d beyond frame height %d", g.Allow.Y, len(lines))
	}
	row := lines[g.Allow.Y]
	if !strings.Contains(row, "allow") || !strings.Contains(row, "deny") {
		t.Errorf("button row %d does not contain the buttons: %q", g.Allow.Y, stripANSI(row))
	}
	// hit-test sanity: a click in the middle of each button resolves correctly
	mid := func(r Rect) (int, int) { return r.X + r.W/2, r.Y }
	for name, r := range map[string]Rect{"allow": g.Allow, "always": g.Always, "deny": g.Deny} {
		x, y := mid(r)
		if got := g.Hit(x, y); got != name {
			t.Errorf("Hit(%d,%d) = %q, want %q", x, y, got, name)
		}
	}
	if got := g.Hit(0, 0); got != "" {
		t.Errorf("Hit(0,0) = %q, want empty", got)
	}
}

func TestPermLayoutMatchesRenderClamped(t *testing.T) {
	// Too narrow for the 43-cell button row (bw < permMinBoxWidth): the buttons
	// would overflow the modal frame / clip at the terminal edge, so hitboxes are
	// disabled (keyboard still works) — a click must not land on a misrendered
	// button. (width 40/48 -> bw 38/46 < 47.)
	for _, sz := range [][2]int{{10, 24}, {40, 24}, {48, 24}} {
		if g := PermLayout(sz[0], sz[1]); g.Active {
			t.Errorf("size %dx%d: expected inactive geometry (too narrow for buttons), got %+v", sz[0], sz[1], g)
		}
	}

	// Wide enough for the buttons, but height clamped: PermLayout is the mouse
	// hit-test and must stay in lockstep with the rendered modal. RenderChat floors
	// height to 8 and centers the modal in a bodyH = height-3 region; if PermLayout
	// applies different clamps the button row/column drift and clicks miss.
	for _, sz := range [][2]int{{50, 11}, {60, 12}, {70, 14}, {52, 24}} {
		w, h := sz[0], sz[1]
		g := PermLayout(w, h)
		if !g.Active {
			t.Fatalf("size %dx%d: expected active geometry, got inactive", w, h)
		}
		out := RenderChat(ChatData{
			Variant: 0, Dark: true, Width: w, Height: h,
			Perm: &Perm{Tool: "edit_file", Risk: "medium", Reason: "writes a file"},
		})
		lines := strings.Split(out, "\n")
		if g.Allow.Y >= len(lines) {
			t.Fatalf("size %dx%d: allow row %d beyond frame height %d", w, h, g.Allow.Y, len(lines))
		}
		row := stripANSI(lines[g.Allow.Y])
		if !strings.Contains(row, "allow") || !strings.Contains(row, "deny") {
			t.Errorf("size %dx%d: button row %d does not contain the buttons: %q", w, h, g.Allow.Y, row)
		}
		// The allow button's rendered column must line up exactly with its hitbox.
		if col := strings.Index(row, "[ a"); col != g.Allow.X+2 {
			t.Errorf("size %dx%d: allow button rendered at col %d, want Allow.X+2 = %d",
				w, h, col, g.Allow.X+2)
		}
		// The rendered button row must be exactly where PermLayout points (no other
		// row carries both labels), and a click in each button's center must resolve.
		for i, ln := range lines {
			pl := stripANSI(ln)
			if i != g.Allow.Y && strings.Contains(pl, "allow") && strings.Contains(pl, "deny") {
				t.Errorf("size %dx%d: buttons also render on row %d, not just %d", w, h, i, g.Allow.Y)
			}
		}
		mid := func(r Rect) (int, int) { return r.X + r.W/2, r.Y }
		for name, r := range map[string]Rect{"allow": g.Allow, "always": g.Always, "deny": g.Deny} {
			x, y := mid(r)
			if got := g.Hit(x, y); got != name {
				t.Errorf("size %dx%d: Hit(%d,%d) = %q, want %q", w, h, x, y, got, name)
			}
		}
	}

	// Even at sub-floor heights (where the modal can't fit its buttons) PermLayout
	// must clamp height like RenderChat so the hitbox never points past the frame.
	for _, h := range []int{1, 4, 7} {
		g := PermLayout(50, h)
		out := RenderChat(ChatData{
			Variant: 0, Dark: true, Width: 50, Height: h,
			Perm: &Perm{Tool: "edit_file", Risk: "medium", Reason: "writes a file"},
		})
		if frameH := strings.Count(out, "\n") + 1; g.Allow.Y >= frameH {
			t.Errorf("height %d: Allow.Y %d points past clamped frame height %d", h, g.Allow.Y, frameH)
		}
	}
}

func TestToolResultRenderingCollapsesAndShows(t *testing.T) {
	d := ChatData{
		Variant: 0, Dark: true, Width: 100, Height: 40,
		Rows: []Row{
			{Kind: "toolcall", Tool: "read_file", Detail: "README.md"},
			{Kind: "toolresult", Tool: "read_file", Status: "ok", Detail: "File: README.md (217 lines)\n\n  1 | THE-RAW-FILE-CONTENT-SHOULD-NOT-APPEAR"},
			{Kind: "toolcall", Tool: "list_directory", Detail: "."},
			{Kind: "toolresult", Tool: "list_directory", Status: "ok", Detail: "Contents of .:\n\na\nb\nc"},
			{Kind: "toolcall", Tool: "edit_file", Detail: "x.go"},
			{Kind: "toolresult", Tool: "edit_file", Status: "ok", Detail: "@@ -1 +1 @@\n-old\n+NEWCODE"},
			{Kind: "toolcall", Tool: "bash", Detail: "go test"},
			{Kind: "toolresult", Tool: "bash", Status: "error", Detail: "exit 1: BUILD-FAILED-HERE"},
		},
	}
	out := stripANSI(RenderChat(d))
	// file read collapses to a count, never dumps content
	if !strings.Contains(out, "217 lines") {
		t.Error("read_file should summarize to a line count")
	}
	if strings.Contains(out, "THE-RAW-FILE-CONTENT-SHOULD-NOT-APPEAR") {
		t.Error("read_file dumped raw file content (should be collapsed)")
	}
	// listing collapses to entry count
	if !strings.Contains(out, "3 entries") {
		t.Error("list_directory should summarize to an entry count")
	}
	// diff body is shown for edits
	if !strings.Contains(out, "NEWCODE") {
		t.Error("edit_file diff body should be shown")
	}
	// errors are surfaced
	if !strings.Contains(out, "BUILD-FAILED-HERE") {
		t.Error("error output should be shown")
	}
}

func TestAssistantMarkdownClipsLongLines(t *testing.T) {
	// glamour does not hard-break unbreakable tokens, so a very long URL or fenced
	// code line would otherwise emit a line far wider than the frame, blowing out
	// the fixed-height/full-bleed layout. Each emitted line must fit the budget.
	s := newStyles(Resolve(0, true), 0, true)
	tw := 60
	longURL := "See https://example.com/" + strings.Repeat("verylongpath/", 40) + "end"
	out := s.renderAssistantMarkdown(longURL, tw)
	if len(out) == 0 {
		t.Fatal("renderAssistantMarkdown returned no lines")
	}
	for i, ln := range out {
		if w := lipgloss.Width(ln); w > tw {
			t.Errorf("line %d width %d exceeds budget %d: %q", i, w, tw, stripANSI(ln))
		}
	}
}

func TestAssistantMarkdownClipsLongFencedCode(t *testing.T) {
	s := newStyles(Resolve(0, true), 0, true)
	tw := 50
	src := "```\n" + strings.Repeat("x", 300) + "\n```"
	out := s.renderAssistantMarkdown(src, tw)
	for i, ln := range out {
		if w := lipgloss.Width(ln); w > tw {
			t.Errorf("line %d width %d exceeds budget %d: %q", i, w, tw, stripANSI(ln))
		}
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc && (r == 'm'):
			inEsc = false
		case inEsc:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestPermLayoutDisablesHitboxesWhenButtonsClipped(t *testing.T) {
	// At a clamped/too-short height the button row can't render; hitboxes must be
	// inactive so a click can't resolve to allow/deny on a non-button row.
	g := PermLayout(80, 8) // height floors to 8 -> bodyH=5 < top+permBtnRow
	if g.Active {
		t.Fatalf("expected inactive geometry when buttons are clipped, got %+v", g)
	}
	// A roomy height still yields active, positioned buttons.
	if big := PermLayout(80, 30); !big.Active || big.Allow.W == 0 {
		t.Fatalf("expected active geometry at full height, got %+v", big)
	}
}

func TestPermModalFitsFrameAtNarrowWidth(t *testing.T) {
	// At a width too narrow for the button row, PermLayout disables the hitboxes
	// and the modal must render a compact keyboard hint — every composed line
	// (chrome + modal) must stay within the frame width so nothing overflows.
	const W = 40
	const H = 24
	if PermLayout(W, H).Active {
		t.Fatalf("PermLayout(%d,%d) should be inactive (too narrow for buttons)", W, H)
	}
	out := stripANSI(RenderChat(ChatData{
		Variant: 0, Dark: true, Width: W, Height: H,
		Perm: &Perm{Tool: "edit_file", Risk: "medium", Reason: "writes a file"},
	}))
	for i, ln := range strings.Split(out, "\n") {
		if lipgloss.Width(ln) > W {
			t.Fatalf("line %d exceeds frame width %d (%d cells): %q", i, W, lipgloss.Width(ln), ln)
		}
	}
	// When the layout is inactive, the command hint must not show mouse/click prompts
	if strings.Contains(out, "click") {
		t.Error("command hint should not contain 'click' when PermLayout is inactive")
	}
}
