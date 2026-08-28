package installtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPullRequestWorkflowsDeclareContentsRead(t *testing.T) {
	root := filepath.Join(repoRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, e := range entries {
		if e.IsDir() || !(strings.HasSuffix(e.Name(), ".yml") || strings.HasSuffix(e.Name(), ".yaml")) {
			continue
		}
		rel := filepath.Join(".github", "workflows", e.Name())
		body := readRepoText(t, rel)
		if !workflowHasPullRequestTrigger(body) {
			continue
		}
		if !workflowHasTopLevelContentsRead(body) {
			missing = append(missing, rel)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("pull_request workflows missing permissions contents: read: %s", strings.Join(missing, ", "))
	}
}

func TestWorkflowPermissionParsing(t *testing.T) {
	tests := []struct {
		name     string
		yml      string
		wantPR   bool
		wantPerm bool
	}{
		{
			name:     "mapping trigger",
			yml:      "on:\n  pull_request:\npermissions:\n  contents: read\n",
			wantPR:   true,
			wantPerm: true,
		},
		{
			name:     "inline trigger list",
			yml:      "on: [pull_request]\npermissions:\n  contents: read\n",
			wantPR:   true,
			wantPerm: true,
		},
		{
			name:     "commented permissions ignored",
			yml:      "on:\n  pull_request:\n# permissions:\n#   contents: read\n",
			wantPR:   true,
			wantPerm: false,
		},
		{
			name:     "nested script text is not a trigger",
			yml:      "on:\n  push:\njobs:\n  x:\n    steps:\n      - run: echo pull_request: permissions: contents: read\n",
			wantPR:   false,
			wantPerm: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := workflowHasPullRequestTrigger(tc.yml); got != tc.wantPR {
				t.Fatalf("trigger = %v, want %v", got, tc.wantPR)
			}
			if got := workflowHasTopLevelContentsRead(tc.yml); got != tc.wantPerm {
				t.Fatalf("permissions = %v, want %v", got, tc.wantPerm)
			}
		})
	}
}

func TestVulncheckWindowsQuotesGobinPath(t *testing.T) {
	body := readRepoText(t, "Makefile")
	for _, want := range []string{
		`mkdir -p "$(CURDIR)/.cache/gobin"`,
		`GOBIN="$(CURDIR)/.cache/gobin"`,
		`"$(CURDIR)/.cache/gobin/govulncheck"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("vulncheck-windows missing quoted path %s", want)
		}
	}
}

func workflowHasPullRequestTrigger(body string) bool {
	inOn := false
	for _, line := range yamlActiveLines(body) {
		indent := yamlIndent(line)
		trim := strings.TrimSpace(line)
		if indent == 0 && trim != "" && !strings.HasPrefix(trim, "-") {
			key, rest, ok := strings.Cut(trim, ":")
			if !ok {
				inOn = false
				continue
			}
			inOn = key == "on"
			if inOn && strings.Contains(rest, "pull_request") {
				return true
			}
			continue
		}
		if inOn && strings.Contains(trim, "pull_request") {
			return true
		}
	}
	return false
}

func workflowHasTopLevelContentsRead(body string) bool {
	inPerm := false
	for _, line := range yamlActiveLines(body) {
		indent := yamlIndent(line)
		trim := strings.TrimSpace(line)
		if indent == 0 && trim != "" && !strings.HasPrefix(trim, "-") {
			key, rest, ok := strings.Cut(trim, ":")
			inPerm = ok && key == "permissions"
			if inPerm && strings.Contains(rest, "contents: read") {
				return true
			}
			continue
		}
		if inPerm && trim == "contents: read" {
			return true
		}
	}
	return false
}

func yamlActiveLines(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

func yamlIndent(line string) int {
	n := 0
	for _, r := range line {
		if r != ' ' && r != '\t' {
			break
		}
		n++
	}
	return n
}
