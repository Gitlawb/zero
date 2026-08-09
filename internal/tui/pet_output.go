package tui

import (
	"bytes"
	"io"
	"sync"

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
	written, err := o.output.Write(value)
	if err != nil {
		return written, err
	}
	if leavingAltScreen {
		if kittyImage {
			if err := o.writeImageUpdate(o.renderer.Clear); err != nil {
				return written, err
			}
		}
		return written, nil
	}
	if err := o.writeImageUpdate(o.renderer.Render); err != nil {
		return written, err
	}
	return written, nil
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
		if err := o.renderer.Clear(writer); err != nil {
			return err
		}
		return o.renderer.DeleteImages(writer, petAmbientImageID, petPreviewImageID)
	})
}
