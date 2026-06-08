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

func TestMessageImagesFieldDefaultsNil(t *testing.T) {
	// A text-only message must leave Images nil (today's behavior, byte-identical).
	textOnly := Message{Role: MessageRoleUser, Content: "hello"}
	if textOnly.Images != nil {
		t.Fatalf("expected nil Images on a text-only message, got %#v", textOnly.Images)
	}

	// The field accepts ImageBlock values carrying raw bytes.
	withImage := Message{
		Role:    MessageRoleUser,
		Content: "look",
		Images: []ImageBlock{
			{MediaType: "image/png", Data: []byte{0x89, 0x50, 0x4e, 0x47}},
		},
	}
	if len(withImage.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(withImage.Images))
	}
	if withImage.Images[0].MediaType != "image/png" {
		t.Fatalf("unexpected media type: %q", withImage.Images[0].MediaType)
	}
	if string(withImage.Images[0].Data) != "\x89PNG" {
		t.Fatalf("unexpected raw data: %q", withImage.Images[0].Data)
	}
}
