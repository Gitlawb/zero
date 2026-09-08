package imageinput

import (
	"errors"
	"testing"
)

// Drives readClipboardImageDarwin's own decision rather than the plumbing around
// it. Both shell steps are substituted, so this runs on any platform and, more
// importantly, actually reaches the branch under test: a test that stubs
// ReadClipboardImage instead stays green with the production change reverted,
// which is how the first version of this coverage slipped through.
func TestReadClipboardImageDarwinDistinguishesUnreadableFromAbsent(t *testing.T) {
	restore := func() func() {
		info, extract := darwinClipboardInfo, darwinClipboardExtract
		return func() { darwinClipboardInfo, darwinClipboardExtract = info, extract }
	}()
	t.Cleanup(restore)

	const pngInfo = `«class PNGf», «class 8BPS»`

	t.Run("image present but no extractor is actionable", func(t *testing.T) {
		darwinClipboardInfo = func() (string, error) { return pngInfo, nil }
		darwinClipboardExtract = func() ([]byte, error) { return nil, errors.New("exit status 127") }

		data, err := readClipboardImageDarwin()
		if !errors.Is(err, ErrClipboardImageUnreadable) {
			t.Fatalf("err = %v, want ErrClipboardImageUnreadable; a stock Mac gets silence instead of a remedy", err)
		}
		if data != nil {
			t.Fatalf("data = %q, want none", data)
		}
	})

	t.Run("extractor producing nothing is also actionable", func(t *testing.T) {
		darwinClipboardInfo = func() (string, error) { return pngInfo, nil }
		darwinClipboardExtract = func() ([]byte, error) { return nil, nil }

		if _, err := readClipboardImageDarwin(); !errors.Is(err, ErrClipboardImageUnreadable) {
			t.Fatalf("err = %v, want ErrClipboardImageUnreadable for an extractor that exits 0 with no bytes", err)
		}
	})

	// The other half of the distinction, and the reason this cannot simply always
	// report an error: an empty clipboard is an ordinary no-op, and turning that
	// into a notice would make every stray Ctrl+V nag the user.
	t.Run("no image on the clipboard stays silent", func(t *testing.T) {
		darwinClipboardInfo = func() (string, error) { return `«class utf8»`, nil }
		darwinClipboardExtract = func() ([]byte, error) {
			t.Fatal("extractor ran despite the clipboard offering no image")
			return nil, nil
		}

		data, err := readClipboardImageDarwin()
		if err != nil || data != nil {
			t.Fatalf("data=%q err=%v, want a silent no-op", data, err)
		}
	})

	t.Run("probe failure stays silent", func(t *testing.T) {
		darwinClipboardInfo = func() (string, error) { return "", errors.New("osascript missing") }
		darwinClipboardExtract = func() ([]byte, error) {
			t.Fatal("extractor ran despite the info probe failing")
			return nil, nil
		}

		if data, err := readClipboardImageDarwin(); err != nil || data != nil {
			t.Fatalf("data=%q err=%v, want a silent no-op", data, err)
		}
	})

	t.Run("working extractor returns the bytes", func(t *testing.T) {
		darwinClipboardInfo = func() (string, error) { return pngInfo, nil }
		darwinClipboardExtract = func() ([]byte, error) { return []byte("png-bytes"), nil }

		data, err := readClipboardImageDarwin()
		if err != nil {
			t.Fatalf("err = %v, want none", err)
		}
		if string(data) != "png-bytes" {
			t.Fatalf("data = %q, want the extractor's bytes", data)
		}
	})
}
