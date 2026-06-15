package browser

import (
	"reflect"
	"testing"
)

func TestOpenCommand(t *testing.T) {
	cases := []struct {
		goos string
		name string
		args []string
	}{
		{"darwin", "open", []string{"https://x.test"}},
		{"linux", "xdg-open", []string{"https://x.test"}},
		{"freebsd", "xdg-open", []string{"https://x.test"}},
		{"windows", "rundll32", []string{"url.dll,FileProtocolHandler", "https://x.test"}},
	}
	for _, tc := range cases {
		t.Run(tc.goos, func(t *testing.T) {
			name, args := openCommand(tc.goos, "https://x.test")
			if name != tc.name || !reflect.DeepEqual(args, tc.args) {
				t.Fatalf("openCommand(%s) = %q %v, want %q %v", tc.goos, name, args, tc.name, tc.args)
			}
		})
	}
}
