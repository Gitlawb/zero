package imageinput

import (
	"bytes"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strings"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// ReadClipboardImage returns the raw image bytes and media type from the OS
// clipboard, or (nil, "", nil) when the clipboard has no image. Called when
// text clipboard is empty (the user pasted a screenshot). The media type is
// sniffed from the bytes, not trusted from the clipboard.
func ReadClipboardImage() ([]byte, string, error) {
	data, err := readClipboardImageBytes()
	if err != nil {
		return nil, "", err
	}
	if data == nil {
		return nil, "", nil
	}
	// Sniff the media type from the bytes — don't trust the clipboard's claim.
	sniffLen := len(data)
	if sniffLen > 512 {
		sniffLen = 512
	}
	mediaType := zeroruntime.NormalizeImageMediaType(http.DetectContentType(data[:sniffLen]))
	if mediaType == "" {
		return nil, "", fmt.Errorf("clipboard image is not a supported type (allowed: png, jpeg, gif, webp)")
	}
	return data, mediaType, nil
}

// readClipboardImageBytes calls the platform-specific clipboard tool to extract
// image bytes. Returns (nil, nil) when no image is present.
func readClipboardImageBytes() ([]byte, error) {
	switch runtime.GOOS {
	case "windows":
		return readClipboardImageWindows()
	case "darwin":
		return readClipboardImageDarwin()
	case "linux":
		return readClipboardImageLinux()
	default:
		return nil, nil
	}
}

// readClipboardImageWindows uses PowerShell to check for and read a clipboard
// image. The image is saved as PNG to a temp file, read back, and the temp file
// deleted. Returns (nil, nil) when no image is on the clipboard.
func readClipboardImageWindows() ([]byte, error) {
	// Check if the clipboard contains an image.
	check := `Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; [System.Windows.Forms.Clipboard]::ContainsImage()`
	out, err := exec.Command("powershell", "-NoProfile", "-Command", check).Output()
	if err != nil {
		return nil, nil // clipboard not available, treat as no image
	}
	if strings.TrimSpace(string(out)) != "True" {
		return nil, nil
	}
	// Read the image as PNG bytes.
	script := `Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; $img = [System.Windows.Forms.Clipboard]::GetImage(); if ($img -ne $null) { $ms = New-Object System.IO.MemoryStream; $img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png); $ms.ToArray() }`
	cmd := exec.Command("powershell", "-NoProfile", "-Command", script)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, nil
	}
	if stdout.Len() == 0 {
		return nil, nil
	}
	return stdout.Bytes(), nil
}

// readClipboardImageDarwin uses osascript to check for and read a clipboard
// image. Returns (nil, nil) when no image is present.
func readClipboardImageDarwin() ([]byte, error) {
	// Check clipboard info for image classes.
	check := `osascript -e 'clipboard info'`
	out, err := exec.Command("sh", "-c", check).Output()
	if err != nil {
		return nil, nil
	}
	info := string(out)
	if !strings.Contains(info, "PNG") && !strings.Contains(info, "JPEG") && !strings.Contains(info, "TIFF") && !strings.Contains(info, "GIF") {
		return nil, nil
	}
	// Write clipboard image to a temp file via AppleScript, then read it.
	// Using pngpaste if available, falling back to a Python one-liner.
	cmd := exec.Command("sh", "-c", `pngpaste - 2>/dev/null || python3 -c "
import AppKit, sys
pb = AppKit.NSPasteboard.generalPasteboard()
data = pb.dataForType_(AppKit.NSPasteboardTypePNG)
if data:
    sys.stdout.buffer.write(data.bytes())
"`)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, nil
	}
	if stdout.Len() == 0 {
		return nil, nil
	}
	return stdout.Bytes(), nil
}

// readClipboardImageLinux tries wl-paste (Wayland) then xclip (X11) to read
// clipboard image bytes. Returns (nil, nil) when no image or no tool available.
func readClipboardImageLinux() ([]byte, error) {
	// Try Wayland first.
	types, err := exec.Command("sh", "-c", "wl-paste --list-types 2>/dev/null").Output()
	if err == nil && len(types) > 0 {
		for _, t := range strings.Split(string(types), "\n") {
			t = strings.TrimSpace(t)
			if strings.HasPrefix(t, "image/") {
				data, err := exec.Command("sh", "-c", "wl-paste --type "+t+" 2>/dev/null").Output()
				if err == nil && len(data) > 0 {
					return data, nil
				}
			}
		}
	}
	// Fall back to X11 xclip.
	types, err = exec.Command("sh", "-c", "xclip -selection clipboard -t TARGETS -o 2>/dev/null").Output()
	if err == nil && len(types) > 0 {
		for _, t := range strings.Split(string(types), "\n") {
			t = strings.TrimSpace(t)
			if strings.HasPrefix(t, "image/") {
				data, err := exec.Command("sh", "-c", "xclip -selection clipboard -t "+t+" -o 2>/dev/null").Output()
				if err == nil && len(data) > 0 {
					return data, nil
				}
			}
		}
	}
	return nil, nil
}
