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
		if !strings.Contains(body, "\npull_request:") && !strings.Contains(body, "\n  pull_request:") {
			continue
		}
		if !strings.Contains(body, "permissions:") || !strings.Contains(body, "contents: read") {
			missing = append(missing, rel)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("pull_request workflows missing permissions contents: read: %s", strings.Join(missing, ", "))
	}
}
