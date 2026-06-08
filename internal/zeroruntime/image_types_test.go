package zeroruntime

import "testing"

func TestNormalizeImageMediaType(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare png", "png", "image/png"},
		{"bare jpeg", "jpeg", "image/jpeg"},
		{"jpg aliases to jpeg", "jpg", "image/jpeg"},
		{"bare gif", "gif", "image/gif"},
		{"bare webp", "webp", "image/webp"},
		{"uppercase trimmed", "  PNG  ", "image/png"},
		{"passthrough image/png", "image/png", "image/png"},
		{"passthrough image/jpeg", "image/jpeg", "image/jpeg"},
		{"passthrough image/gif", "image/gif", "image/gif"},
		{"passthrough image/webp", "image/webp", "image/webp"},
		{"image/jpg aliases to jpeg", "image/jpg", "image/jpeg"},
		{"data uri png stripped", "data:image/png;base64,iVBORw0KGgo=", "image/png"},
		{"data uri jpg stripped and aliased", "data:image/jpg;base64,AAAA", "image/jpeg"},
		{"data uri bare jpg", "data:jpg;base64,AAAA", "image/jpeg"},
		{"reject svg", "image/svg+xml", ""},
		{"reject bmp", "bmp", ""},
		{"reject empty", "", ""},
		{"reject non-image mime", "text/plain", ""},
		{"reject bare unknown", "tiff", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeImageMediaType(tc.in); got != tc.want {
				t.Fatalf("NormalizeImageMediaType(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
