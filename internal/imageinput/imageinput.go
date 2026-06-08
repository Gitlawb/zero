// Package imageinput is the single shared loader for local image files used by
// every input surface (CLI exec --image, TUI /image). It reads a file, sniffs +
// normalizes its media type against the allow-list, enforces the per-image size
// cap, and returns a raw-bytes ImageBlock. Keeping it here means the CLI and TUI
// never duplicate the read/sniff/normalize/cap logic.
package imageinput

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// MaxImageBytes is the per-image decoded-size cap (10 MiB). Bytes above this are
// rejected at every input boundary so an unbounded request body never reaches a
// provider.
const MaxImageBytes = 10 << 20

// LoadFile reads the image at path (resolved against workspaceRoot when
// relative), validates its type and size, and returns a raw-bytes ImageBlock.
// Errors are plain (callers wrap them into surface-specific usage/notice text).
func LoadFile(path string, workspaceRoot string) (zeroruntime.ImageBlock, error) {
	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(workspaceRoot, resolved)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return zeroruntime.ImageBlock{}, fmt.Errorf("image file not found: %s", path)
	}

	if len(data) > MaxImageBytes {
		return zeroruntime.ImageBlock{}, fmt.Errorf("image %s is larger than the 10 MiB limit", path)
	}

	sniffLen := len(data)
	if sniffLen > 512 {
		sniffLen = 512
	}
	mediaType := zeroruntime.NormalizeImageMediaType(http.DetectContentType(data[:sniffLen]))
	if mediaType == "" {
		return zeroruntime.ImageBlock{}, fmt.Errorf("unsupported image type for %s (allowed: png, jpeg, gif, webp)", path)
	}

	return zeroruntime.ImageBlock{MediaType: mediaType, Data: data}, nil
}
