package tui

import (
	"path/filepath"
	"strings"

	"github.com/Gitlawb/zero/internal/imageinput"
	"github.com/Gitlawb/zero/internal/modelregistry"
)

// modelSupportsVisionTUI reports whether the active model can accept image input.
// An unknown / custom id (not in the catalog) returns false: we cannot confirm
// vision support, so the TUI refuses to attach rather than silently sending
// images a model may reject. Mirrors the CLI/headless vision gate (component E).
func modelSupportsVisionTUI(modelName string) bool {
	trimmed := strings.TrimSpace(modelName)
	if trimmed == "" {
		return false
	}
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		return false
	}
	return modelregistry.SupportsVision(registry, trimmed)
}

// handleImageCommand processes "/image <path>" and "/image clear". A bare
// "/image" prints usage. Attaching to a non-vision model is refused inline (the
// active model can be checked synchronously). Attachment failures (missing file,
// unsupported type, oversize) surface as an inline notice and attach nothing.
func (m model) handleImageCommand(arg string) model {
	trimmed := strings.TrimSpace(arg)
	switch {
	case trimmed == "":
		return m.appendImageNotice("Usage: /image <path>  (or /image clear)")
	case strings.EqualFold(trimmed, "clear"):
		m.pendingImages = nil
		m.pendingImageLabels = nil
		return m.appendImageNotice("Cleared pending image attachments.")
	}

	if !modelSupportsVisionTUI(m.modelName) {
		name := m.modelName
		if name == "" {
			name = "the active model"
		}
		return m.appendImageNotice("Model " + name + " does not support image input; attachment refused.")
	}

	block, err := imageinput.LoadFile(trimmed, m.cwd)
	if err != nil {
		return m.appendImageNotice(err.Error())
	}

	m.pendingImages = append(m.pendingImages, block)
	m.pendingImageLabels = append(m.pendingImageLabels, filepath.Base(trimmed))
	return m.appendImageNotice("Attached " + filepath.Base(trimmed) + " (" + block.MediaType + ").")
}

func (m model) appendImageNotice(text string) model {
	return m.appendSystemNotice(text)
}

// renderImageChips builds a one-line "[img: a.png] [img: b.png]" row for the
// pending attachments, or "" when there are none. Kept plain so the renderer
// can wrap/style it consistently.
func renderImageChips(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	chips := make([]string, 0, len(labels))
	for _, label := range labels {
		chips = append(chips, "[img: "+label+"]")
	}
	return strings.Join(chips, " ")
}
