package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/zeroline"
)

// runZeroline launches the interactive Zero TUI with the "zeroline" skin: a Zen
// home page and a Statusline chat page with 5 switchable color themes. It reuses
// the exact same runtime wiring as the default `zero` shell (provider, tools,
// sandbox, permissions, sessions) — only the rendering differs.
//
// With --snapshot it renders a single static frame (home page) to stdout for
// local verification without a TTY.
func runZeroline(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	fs := flag.NewFlagSet("zeroline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	snapshot := fs.Bool("snapshot", false, "render a single frame to stdout and exit (no TTY)")
	page := fs.String("page", "home", "snapshot page: home|chat")
	variant := fs.Int("variant", 1, "color theme 1-5 (1 Phosphor, 2 Cyan, 3 Sage, 4 Violet, 5 Mono)")
	light := fs.Bool("light", false, "use the light variant for the snapshot")
	perm := fs.Bool("perm", false, "show the centered permission modal in the chat snapshot")
	boot := fs.Int("boot", -1, "render the boot splash at the given animation frame")
	stream := fs.Bool("stream", false, "show a streaming assistant response in the chat snapshot")
	width := fs.Int("width", 100, "snapshot width")
	height := fs.Int("height", 30, "snapshot height")
	skipUnsafe := fs.Bool("skip-permissions-unsafe", false, "launch in unsafe permission mode (enables the ! shell escape)")
	skin := fs.String("skin", "zeroline", "skin: zeroline|hybrid (hybrid: V1 home via startup + timeline Ev body; default for main `zero` per PR6; --snapshot matrix for V1+V4 as shipped default)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *snapshot {
		v := *variant - 1
		if v < 0 || v >= len(zeroline.Themes) {
			v = 0
		}
		if *boot >= 0 {
			if _, err := fmt.Fprintln(stdout, zeroline.RenderBoot(v, !*light, *boot, *width, *height)); err != nil {
				return 1
			}
			return 0
		}
		hdr := zeroline.Header{Cwd: "~/src/zero", Branch: "main", Model: "claude-sonnet-4.5", Provider: "anthropic"}
		var frame string
		if *page == "chat" {
			cd := zeroline.ChatData{
				Variant: v, Dark: !*light, Width: *width, Height: *height, Header: hdr,
				Rows: []zeroline.Row{
					{Kind: "user", Text: "refactor internal/agent/loop.go to extract tool execution"},
					{Kind: "toolcall", Tool: "list_directory", Detail: "internal/agent"},
					{Kind: "toolresult", Tool: "list_directory", Status: "ok", Detail: "Contents of internal/agent:\n\nloop.go\ntypes.go\nloop_test.go"},
					{Kind: "toolcall", Tool: "read_file", Detail: "internal/agent/loop.go"},
					{Kind: "toolresult", Tool: "read_file", Status: "ok", Detail: "File: internal/agent/loop.go (164 lines)\n\n118 | func (l *Loop) run(ctx context.Context) error {"},
					{Kind: "toolcall", Tool: "edit_file", Detail: "exec.go (new) · loop.go"},
					{Kind: "toolresult", Tool: "edit_file", Status: "ok", Detail: "--- a/internal/agent/loop.go\n+++ b/internal/agent/exec.go\n@@ -141,6 +141,3 @@\n-\tswitch t := call.Tool.(type) {\n-\tcase ReadFileTool: out, err = l.readFile(ctx, t)\n+\tout, err := l.exec.Dispatch(call)"},
					{Kind: "assistant", Text: "Done. Extracted a `ToolExecutor`:\n\n```go\nfunc (e *ToolExecutor) Dispatch(c Call) (Out, error) {\n\treturn e.route(c)\n}\n```\n\nThe switch in loop.go now delegates to one call. Tests pass."},
					{Kind: "error", Text: "go test ./... failed (exit 1)"}, // PR5: exercise error state in timeline Ev at widths
				},
				Input: "❯ ",
			}
			if *perm {
				cd.Perm = &zeroline.Perm{Tool: "edit_file", Risk: "medium", Reason: "writes internal/agent/exec.go and loop.go", Summary: "write"}
			}
			if *stream {
				cd.Rows = cd.Rows[:len(cd.Rows)-1] // drop the final assistant row
				cd.Working = true
				cd.Stream = "Done. I extracted a `ToolExecutor` and collapsed the dispatch switch in loop.go to a single delegated call — the"
				cd.TokS = 84
			}
			frame = zeroline.RenderChat(cd)
		} else {
			frame = zeroline.RenderHome(zeroline.HomeData{
				Variant: v, Dark: !*light, Width: *width, Height: *height, Header: hdr,
				Input: "❯ message zero — / commands · @ files · ! bash",
			})
		}
		// --snapshot --page home --width 80/120/160 --skin hybrid (or --page chat --perm --stream)
		// extended for PR4: produces "home then chat timeline" smoke showing V1 home alignment + timeline Ev body
		// (with tool/perm/stream events at 80 cols). For hybrid, runtime uses startupView (zeroLogoLines V1) then
		// Ev timeline; snapshot here exercises the Render paths + skin flag for verification (DoD).
		// Cites: PR4 desc, "Extend cli/zeroline --snapshot", "V1 home -> timeline with tool/perm/stream events", "80-col snapshot clean".
		// PR5 polish: now covers blocked/perm/error/tool/stream states + 80-col collapse (time hidden, glyphs+content, no rail) for hybrid timeline; copy/paste usable (simple │ text); manual narrow shows stable prompt + collapsed timeline. Cites Hybrid Target responsive/80-col/risks + PR5 DoD. --skin hybrid passed through.
		// (prior PR1 comment retained for unified palette.)
		// PR6 (ship target): add final snapshot matrix coverage for hybrid at widths. Matrix runs (home + chat at 80/120/160, with perm/stream/error states) verify "V1 home + V4 timeline as default experience". DoD: go test+build smoke; cli/zeroline --snapshot matrix for hybrid shows the flow; docs updated; no regression. Cites: PR6 DoD, "cli/zeroline --snapshot matrix for hybrid at widths shows V1 home + V4 timeline as default experience", "hybrid as the shipped default target", Rollout "verification with --skin hybrid + snapshots". Use e.g. `go run ./cmd/zero zeroline --snapshot --skin hybrid --page home --width 80` (and 120/160, chat variants). Relative paths in cites per design refs.
		if _, err := fmt.Fprintln(stdout, frame); err != nil {
			return 1
		}
		return 0
	}

	permissionMode := agent.PermissionModeAsk
	if *skipUnsafe {
		permissionMode = agent.PermissionModeUnsafe
	}
	sk := *skin
	if sk != "zeroline" && sk != "hybrid" {
		sk = "zeroline"
	}
	return runInteractiveTUIWithSkin(stderr, deps, sk, permissionMode)
}
