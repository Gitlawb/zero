package minify

import (
	"strings"
	"testing"
)

func TestJSONToTOON(t *testing.T) {
	jsonInput := `{"users": [{"id": 1, "name": "Ada"}, {"id": 2, "name": "Linus"}]}`
	result, err := JSONToTOON([]byte(jsonInput))
	if err != nil {
		t.Fatalf("JSONToTOON failed: %v", err)
	}

	if !strings.Contains(result, "```toon") {
		t.Errorf("expected result to contain ```toon code block, got:\n%s", result)
	}
	if !strings.Contains(result, "users") {
		t.Errorf("expected result to contain 'users', got:\n%s", result)
	}
}
