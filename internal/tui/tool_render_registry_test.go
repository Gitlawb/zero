package tui

import (
	"strings"
	"testing"
)

func TestDefaultToolBodyRegistrySelectsCoreRenderers(t *testing.T) {
	registry := newDefaultToolBodyRegistry()
	opts := cardRenderOptions{bodyCap: cardBodyMaxLines}

	tests := []struct {
		name   string
		hint   string
		detail string
		want   []string
	}{
		{
			name: "edit_file",
			detail: strings.Join([]string{
				"--- a/app.go",
				"+++ b/app.go",
				"@@ -1 +1 @@",
				"-old",
				"+new",
			}, "\n"),
			want: []string{"app.go", "-1", "+1", "new"},
		},
		{
			name: "apply_patch",
			detail: strings.Join([]string{
				"--- a/app.go",
				"+++ b/app.go",
				"@@ -1 +1 @@",
				"-old",
				"+new",
			}, "\n"),
			want: []string{"app.go", "-1", "+1", "new"},
		},
		{
			name:   "read_file",
			detail: "File: README.md\n\n  7 | # Zero",
			want:   []string{"# Zero", "L7"},
		},
		{
			name:   "bash",
			hint:   "go test ./internal/tui",
			detail: "stdout:\nok\nexit_code: 0",
			want:   []string{"go test ./internal/tui", "ok", "exit 0"},
		},
		{
			name:   "grep",
			detail: "internal/tui/rendering.go:41: func render()",
			want:   []string{"internal/tui/rendering.go:41", "1 matches"},
		},
		{
			name:   "unknown_tool",
			detail: "raw output",
			want:   []string{"raw output"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := registry.render(toolBodyRequest{
				name:   tt.name,
				hint:   tt.hint,
				detail: normalizeToolCardDetail(tt.detail),
				width:  96,
				opts:   opts,
			})
			got := plainRender(t, strings.Join(append(append([]string{}, body.lines...), body.headTag, body.footer), "\n"))
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("%s body = %q, missing %q", tt.name, got, want)
				}
			}
		})
	}
}

func TestToolBodyRegistryReplacementIsScopedToOneTool(t *testing.T) {
	registry := newDefaultToolBodyRegistry()
	registry.register("grep", toolBodyRendererFunc(func(req toolBodyRequest) cardBody {
		return cardBody{lines: []string{zeroTheme.onPanel(zeroTheme.ink).Render("replacement grep body")}}
	}))

	opts := cardRenderOptions{bodyCap: cardBodyMaxLines}
	grepBody := registry.render(toolBodyRequest{
		name:   "grep",
		detail: "internal/tui/rendering.go:41: func render()",
		width:  96,
		opts:   opts,
	})
	if got := plainRender(t, strings.Join(grepBody.lines, "\n")); !strings.Contains(got, "replacement grep body") {
		t.Fatalf("grep replacement body = %q, want replacement", got)
	}

	bashBody := registry.render(toolBodyRequest{
		name:   "bash",
		hint:   "go test ./internal/tui",
		detail: normalizeToolCardDetail("stdout:\nok\nexit_code: 0"),
		width:  96,
		opts:   opts,
	})
	got := plainRender(t, strings.Join(append(append([]string{}, bashBody.lines...), bashBody.footer), "\n"))
	if strings.Contains(got, "replacement grep body") {
		t.Fatalf("bash body = %q, must not use grep replacement", got)
	}
	if !strings.Contains(got, "go test ./internal/tui") || !strings.Contains(got, "exit 0") {
		t.Fatalf("bash body = %q, want original bash renderer", got)
	}
}

func TestToolBodyRegistryTrimsRegisteredNames(t *testing.T) {
	registry := newToolBodyRegistry(unknownToolBodyRenderer{})
	registry.register(" grep ", toolBodyRendererFunc(func(req toolBodyRequest) cardBody {
		return cardBody{lines: []string{zeroTheme.onPanel(zeroTheme.ink).Render("trimmed grep body")}}
	}))

	body := registry.render(toolBodyRequest{
		name:   "grep",
		detail: "internal/tui/rendering.go:41: func render()",
		width:  96,
		opts:   cardRenderOptions{bodyCap: cardBodyMaxLines},
	})

	if got := plainRender(t, strings.Join(body.lines, "\n")); !strings.Contains(got, "trimmed grep body") {
		t.Fatalf("grep body = %q, want trimmed registered renderer", got)
	}
}
