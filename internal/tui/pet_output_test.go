package tui

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/terminalpet"
)

func TestPetImageOutputAppendsScheduledImageAfterFrame(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "pet-output-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	frame := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	frame.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	animation, _ := terminalpet.ThumbnailAnimation(frame)
	renderer := terminalpet.NewImageRenderer(terminalpet.ImageSupport{Protocol: terminalpet.ImageProtocolKitty})
	renderer.Set(&terminalpet.ImageDraw{ID: 7, Animation: animation, State: terminalpet.Idle, Columns: 4, Rows: 3})
	output := newPetImageOutput(file, renderer)
	if _, err := output.Write([]byte("bubbletea-frame")); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.HasPrefix(got, "bubbletea-frame") || !strings.Contains(got, "_Ga=T,t=d,f=100,c=4,r=3") {
		t.Fatalf("output did not append image after frame: %q", got)
	}
	syncStart := strings.Index(got, "\x1b[?2026h")
	imageAt := strings.Index(got, "_Ga=T,t=d,f=100,c=4,r=3")
	syncEnd := strings.LastIndex(got, "\x1b[?2026l")
	if syncStart < 0 || imageAt < syncStart || syncEnd < imageAt {
		t.Fatalf("image update was not synchronized: %q", got)
	}
}

func TestPetImageOutputClearsKittyImageAfterLeavingAltScreen(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "pet-output-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	frame := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	animation, _ := terminalpet.ThumbnailAnimation(frame)
	renderer := terminalpet.NewImageRenderer(terminalpet.ImageSupport{Protocol: terminalpet.ImageProtocolKitty})
	renderer.Set(&terminalpet.ImageDraw{ID: 7, Animation: animation, State: terminalpet.Idle, Columns: 4, Rows: 3})
	output := newPetImageOutput(file, renderer)
	if _, err := output.Write([]byte("frame")); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("\x1b[?1049l")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	deleteAt := strings.LastIndex(got, "_Ga=d,d=I,i=7,q=2")
	exitAt := strings.LastIndex(got, "\x1b[?1049l")
	if deleteAt < 0 || exitAt < 0 || deleteAt < exitAt {
		t.Fatalf("Kitty image was not cleared after alt-screen exit: %q", got)
	}
	syncStart := strings.LastIndex(got[:deleteAt], "\x1b[?2026h")
	syncEnd := strings.Index(got[deleteAt:], "\x1b[?2026l")
	if syncStart < exitAt || syncEnd < 0 {
		t.Fatalf("Kitty image cleanup was not synchronized after alt-screen exit: %q", got)
	}
}

func TestPetImageOutputFinalCleanupRepeatsAllKittyDeletes(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "pet-output-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	frame := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	animation, _ := terminalpet.ThumbnailAnimation(frame)
	renderer := terminalpet.NewImageRenderer(terminalpet.ImageSupport{Protocol: terminalpet.ImageProtocolKitty})
	renderer.Set(&terminalpet.ImageDraw{ID: petAmbientImageID, Animation: animation, State: terminalpet.Idle, Columns: 4, Rows: 3})
	output := newPetImageOutput(file, renderer)
	if _, err := output.Write([]byte("frame")); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("\x1b[?1049l")); err != nil {
		t.Fatal(err)
	}
	cleanupStart, err := file.Seek(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := output.clearImage(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	cleanup := string(data[cleanupStart:])
	for _, id := range []uint32{petAmbientImageID, petPreviewImageID} {
		want := fmt.Sprintf("_Ga=d,d=I,i=%d,q=2", id)
		if !strings.Contains(cleanup, want) {
			t.Errorf("final cleanup did not repeat Kitty delete for image %d: %q", id, cleanup)
		}
	}
}

func TestPetImageOutputClearsSixelBeforeLeavingAltScreen(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "pet-output-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	frame := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	animation, _ := terminalpet.ThumbnailAnimation(frame)
	renderer := terminalpet.NewImageRenderer(terminalpet.ImageSupport{Protocol: terminalpet.ImageProtocolSixel})
	renderer.Set(&terminalpet.ImageDraw{ID: 7, Animation: animation, State: terminalpet.Idle, X: 2, Y: 3, Columns: 4, Rows: 3, HeightPixels: 12})
	output := newPetImageOutput(file, renderer)
	if _, err := output.Write([]byte("frame")); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("\x1b[?1049l")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	clearAt := strings.LastIndex(got, "\x1b[4;3H    ")
	exitAt := strings.LastIndex(got, "\x1b[?1049l")
	if clearAt < 0 || exitAt < 0 || clearAt > exitAt {
		t.Fatalf("Sixel cell area was not cleared before alt-screen exit: %q", got)
	}
}
