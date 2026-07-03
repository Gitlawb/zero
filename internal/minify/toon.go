package minify

import (
	"encoding/json"
	"fmt"

	"github.com/toon-format/toon-go"
)

// JSONToTOON parses JSON data and serializes it to a tab-separated TOON format,
// wrapped in an LLM-friendly fenced markdown code block.
func JSONToTOON(jsonBytes []byte) (string, error) {
	var parsed any
	if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
		return "", err
	}

	// Marshal to TOON using tabs for array delimiters to minimize tokens
	toonBytes, err := toon.Marshal(parsed,
		toon.WithArrayDelimiter(toon.DelimiterTab),
		toon.WithLengthMarkers(true),
		toon.WithIndent(2),
	)
	if err != nil {
		return "", err
	}

	// Format for the LLM
	output := fmt.Sprintf("Data is in TOON format (tab-separated, arrays show length and fields):\n```toon\n%s\n```", string(toonBytes))
	return output, nil
}
