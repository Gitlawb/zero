// Test seams: helpers only test code uses, kept out of the production binary.
package providerio

import (
	"bufio"
	"io"
)

// ScanSSEData parses Server-Sent Event data fields from a streaming response.
func ScanSSEData(reader io.Reader, handle func(data string) bool) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 4096), maxSSELineBytes)
	return scanSSEPayloads(scanner, handle, nil)
}
