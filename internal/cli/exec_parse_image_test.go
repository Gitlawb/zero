package cli

import (
	"strings"
	"testing"
)

func TestParseExecImageFlagRepeatable(t *testing.T) {
	options, help, err := parseExecArgs([]string{
		"--image", "one.png",
		"--image=two.jpg",
		"describe these",
	})
	if err != nil {
		t.Fatalf("parseExecArgs returned error: %v", err)
	}
	if help {
		t.Fatal("help = true, want false")
	}
	want := []string{"one.png", "two.jpg"}
	if len(options.imagePaths) != len(want) {
		t.Fatalf("imagePaths = %#v, want %#v", options.imagePaths, want)
	}
	for i := range want {
		if options.imagePaths[i] != want[i] {
			t.Fatalf("imagePaths[%d] = %q, want %q", i, options.imagePaths[i], want[i])
		}
	}
	if strings.Join(options.promptParts, " ") != "describe these" {
		t.Fatalf("promptParts = %#v", options.promptParts)
	}
}

func TestParseExecImageFlagRequiresValue(t *testing.T) {
	if _, _, err := parseExecArgs([]string{"--image"}); err == nil ||
		!strings.Contains(err.Error(), "--image requires a value") {
		t.Fatalf("expected --image requires a value error, got %v", err)
	}
}
