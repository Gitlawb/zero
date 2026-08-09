package terminalpet

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
)

const (
	DisabledID         = "disabled"
	DefaultManifestURL = "https://petdex.dev/api/manifest/v2"
	DefaultRankingURL  = "https://petdex.dev/api/pets/search?sort=installed&limit=60&includeMeta=0"
	TrustedAssetHost   = "assets.petdex.dev"
	previewFrameCount  = 6
	atlasColumns       = 8
)

type State string

const (
	Idle    State = "idle"
	Running State = "running"
	Waiting State = "waiting"
	Review  State = "review"
	Failed  State = "failed"
)

type Entry struct {
	Slug           string `json:"slug"`
	DisplayName    string `json:"displayName"`
	Kind           string `json:"kind,omitempty"`
	SubmittedBy    string `json:"submittedBy,omitempty"`
	SpritesheetURL string `json:"spritesheet"`
	PetJSONURL     string `json:"petJson,omitempty"`
	AssetBase      string `json:"assetBase,omitempty"`
	SpriteVersion  int    `json:"spriteVersionNumber"`
	Local          bool   `json:"-"`
}

func (e Entry) Label() string {
	if name := strings.TrimSpace(e.DisplayName); name != "" {
		return name
	}
	return e.Slug
}

type Animation struct {
	frames   map[State][]image.Image
	pngMu    sync.Mutex
	pngCache map[frameCacheKey][]byte
}

type frameCacheKey struct {
	state State
	index int
}

func (a *Animation) Frame(state State, phase int) image.Image {
	frame, _ := a.frame(state, phase)
	return frame
}

func (a *Animation) frame(state State, phase int) (image.Image, frameCacheKey) {
	if a == nil {
		return nil, frameCacheKey{}
	}
	frames := a.frames[state]
	if len(frames) == 0 {
		state = Idle
		frames = a.frames[Idle]
	}
	if len(frames) == 0 {
		return nil, frameCacheKey{}
	}
	if phase < 0 {
		phase = -phase
	}
	index := phase % len(frames)
	return frames[index], frameCacheKey{state: state, index: index}
}

func (a *Animation) framePNG(state State, phase int) ([]byte, frameCacheKey, error) {
	frame, key := a.frame(state, phase)
	if frame == nil {
		return nil, key, fmt.Errorf("pet animation has no frames")
	}
	a.pngMu.Lock()
	defer a.pngMu.Unlock()
	if cached := a.pngCache[key]; len(cached) > 0 {
		return cached, key, nil
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, frame); err != nil {
		return nil, key, fmt.Errorf("encode pet frame: %w", err)
	}
	if a.pngCache == nil {
		a.pngCache = make(map[frameCacheKey][]byte)
	}
	value := append([]byte(nil), encoded.Bytes()...)
	a.pngCache[key] = value
	return value, key, nil
}

func PreviewAnimation(sheet image.Image) (*Animation, error) {
	if sheet == nil {
		return nil, fmt.Errorf("preview image is empty")
	}
	bounds := sheet.Bounds()
	if bounds.Dx() < previewFrameCount || bounds.Dx()%previewFrameCount != 0 || bounds.Dy() < 1 {
		return nil, fmt.Errorf("preview must contain %d equal-width frames", previewFrameCount)
	}
	frames := splitRow(sheet, previewFrameCount, 0, bounds.Dy())
	return &Animation{frames: map[State][]image.Image{Idle: frames}}, nil
}

func ThumbnailAnimation(imageValue image.Image) (*Animation, error) {
	if imageValue == nil || imageValue.Bounds().Empty() {
		return nil, fmt.Errorf("thumbnail image is empty")
	}
	return &Animation{frames: map[State][]image.Image{Idle: []image.Image{imageValue}}}, nil
}

func AtlasAnimation(sheet image.Image, spriteVersion int) (*Animation, error) {
	if sheet == nil {
		return nil, fmt.Errorf("spritesheet is empty")
	}
	rows := 9
	if spriteVersion == 2 {
		rows = 11
	} else if spriteVersion != 0 && spriteVersion != 1 {
		return nil, fmt.Errorf("unsupported sprite version %d", spriteVersion)
	}
	bounds := sheet.Bounds()
	if bounds.Dx()%atlasColumns != 0 || bounds.Dy()%rows != 0 {
		return nil, fmt.Errorf("spritesheet must be an 8x%d grid", rows)
	}
	frameWidth := bounds.Dx() / atlasColumns
	frameHeight := bounds.Dy() / rows
	if frameWidth < 1 || frameHeight < 1 || frameWidth*208 != frameHeight*192 {
		return nil, fmt.Errorf("spritesheet frames must use the 192x208 aspect ratio")
	}
	stateRows := map[State]struct {
		row   int
		count int
	}{
		Idle:    {row: 0, count: 6},
		Failed:  {row: 5, count: 8},
		Waiting: {row: 6, count: 6},
		Running: {row: 7, count: 6},
		Review:  {row: 8, count: 6},
	}
	frames := make(map[State][]image.Image, len(stateRows))
	for state, spec := range stateRows {
		frames[state] = splitRow(sheet, atlasColumns, spec.row*frameHeight, frameHeight)[:spec.count]
	}
	return &Animation{frames: frames}, nil
}

func splitRow(sheet image.Image, count, y, height int) []image.Image {
	bounds := sheet.Bounds()
	width := bounds.Dx() / count
	frames := make([]image.Image, 0, count)
	for index := 0; index < count; index++ {
		frames = append(frames, cropImage{
			source: sheet,
			bounds: image.Rect(0, 0, width, height),
			offset: image.Pt(bounds.Min.X+index*width, bounds.Min.Y+y),
		})
	}
	return frames
}

type cropImage struct {
	source image.Image
	bounds image.Rectangle
	offset image.Point
}

func (c cropImage) ColorModel() color.Model { return c.source.ColorModel() }
func (c cropImage) Bounds() image.Rectangle { return c.bounds }
func (c cropImage) At(x, y int) color.Color {
	return c.source.At(x+c.offset.X, y+c.offset.Y)
}
