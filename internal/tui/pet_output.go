package tui

import (
	"bytes"
	"io"
	"sync"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gitlawb/zero/internal/terminalpet"
)

const (
	terminalSyncStart = "\x1b[?2026h"
	terminalSyncEnd   = "\x1b[?2026l"
)

type terminalOutputFile interface {
	io.ReadWriteCloser
	Fd() uintptr
}

type petImageOutput struct {
	output   terminalOutputFile
	renderer *terminalpet.ImageRenderer
	mu       sync.Mutex
}

func newPetImageOutput(output terminalOutputFile, renderer *terminalpet.ImageRenderer) *petImageOutput {
	return &petImageOutput{output: output, renderer: renderer}
}

func (o *petImageOutput) Read(value []byte) (int, error) {
	return o.output.Read(value)
}

func (o *petImageOutput) Write(value []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	leavingAltScreen := bytes.Contains(value, []byte("\x1b[?1049l"))
	kittyImage := o.renderer.Support().Protocol == terminalpet.ImageProtocolKitty ||
		o.renderer.Support().Protocol == terminalpet.ImageProtocolKittyLocalFile
	if leavingAltScreen && !kittyImage {
		if err := o.writeImageUpdate(o.renderer.Clear); err != nil {
			return 0, err
		}
	}
	if leavingAltScreen {
		written, err := o.output.Write(value)
		if err != nil {
			return written, err
		}
		if kittyImage {
			if err := o.writeImageUpdate(o.renderer.Clear); err != nil {
				return written, err
			}
		}
		return written, nil
	}
	if bytes.Contains(value, []byte(ansi.EraseEntireScreen)) ||
		bytes.Contains(value, []byte(ansi.SetModeAltScreenSaveCursor)) {
		o.renderer.Invalidate()
	}

	var imageUpdate bytes.Buffer
	if err := o.renderer.Render(&imageUpdate); err != nil {
		return 0, err
	}
	if imageUpdate.Len() == 0 {
		return o.output.Write(value)
	}

	// Bubble Tea encloses supported terminal frames in synchronized-output
	// markers. Keep the pet placement inside that same transaction so the
	// terminal never presents the text frame and image movement separately.
	if syncEnd := bytes.LastIndex(value, []byte(terminalSyncEnd)); syncEnd >= 0 {
		var frame bytes.Buffer
		frame.Grow(len(value) + imageUpdate.Len())
		frame.Write(value[:syncEnd])
		frame.Write(imageUpdate.Bytes())
		frame.Write(value[syncEnd:])
		written, err := o.output.Write(frame.Bytes())
		consumed := originalBytesWritten(written, syncEnd, imageUpdate.Len(), len(value))
		if err == nil && written != frame.Len() {
			err = io.ErrShortWrite
		}
		if err != nil {
			return consumed, err
		}
		return len(value), nil
	}

	if _, err := io.WriteString(o.output, terminalSyncStart); err != nil {
		return 0, err
	}
	written, writeErr := o.output.Write(value)
	if writeErr == nil {
		_, writeErr = o.output.Write(imageUpdate.Bytes())
	}
	_, endErr := io.WriteString(o.output, terminalSyncEnd)
	if writeErr != nil {
		return written, writeErr
	}
	return written, endErr
}

// originalBytesWritten maps progress through an expanded synchronized frame
// back to bytes consumed from the caller's original buffer. Bytes belonging
// to the injected image update must never be reported as caller bytes.
func originalBytesWritten(written, prefixLength, injectedLength, originalLength int) int {
	if written <= 0 {
		return 0
	}
	if written <= prefixLength {
		return written
	}
	consumed := prefixLength
	suffixWritten := written - prefixLength - injectedLength
	if suffixWritten > 0 {
		consumed += min(suffixWritten, originalLength-prefixLength)
	}
	return min(consumed, originalLength)
}

func (o *petImageOutput) writeImageUpdate(render func(io.Writer) error) error {
	var update bytes.Buffer
	if err := render(&update); err != nil {
		return err
	}
	if update.Len() == 0 {
		return nil
	}
	if _, err := io.WriteString(o.output, terminalSyncStart); err != nil {
		return err
	}
	_, writeErr := o.output.Write(update.Bytes())
	_, endErr := io.WriteString(o.output, terminalSyncEnd)
	if writeErr != nil {
		return writeErr
	}
	return endErr
}

func (o *petImageOutput) Close() error {
	return nil
}

func (o *petImageOutput) Fd() uintptr {
	return o.output.Fd()
}

func (o *petImageOutput) clearImage() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.writeImageUpdate(func(writer io.Writer) error {
		if _, err := io.WriteString(writer, ansi.ResetModeMouseExtSgrPixel); err != nil {
			return err
		}
		if err := o.renderer.Clear(writer); err != nil {
			return err
		}
		return o.renderer.DeleteImages(writer, petAmbientImageID, petPreviewImageID)
	})
}
