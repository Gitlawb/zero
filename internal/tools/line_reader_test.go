package tools

import (
	"bufio"
	"strings"
	"testing"
)

func TestReadRawLineLimitedCRLFAtLimit(t *testing.T) {
	for _, test := range []struct {
		name    string
		maxKeep int
		want    string
		clipped bool
	}{
		{name: "content plus CRLF", maxKeep: 10, want: "abcdefghij", clipped: false},
		{name: "content plus CRLF over limit", maxKeep: 11, want: "abcdefghij", clipped: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			line, ended, clipped, err := readRawLineLimited(bufio.NewReader(strings.NewReader("abcdefghij\r\n")), test.maxKeep)
			if err != nil {
				t.Fatal(err)
			}
			if string(line) != test.want || !ended || clipped != test.clipped {
				t.Fatalf("line=%q ended=%v clipped=%v", line, ended, clipped)
			}
		})
	}
}
