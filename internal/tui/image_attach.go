package tui

import (
	"fmt"
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
// "/image" prints usage. PDFs are routed to the document path (text layer always
// attaches; pages rasterize to images only for vision models with a rasterizer).
// Image files attach only to vision models. Attachment failures (missing file,
// unsupported type, oversize) surface as an inline notice and attach nothing.
func (m model) handleImageCommand(arg string) model {
	trimmed := strings.TrimSpace(arg)
	switch {
	case trimmed == "":
		return m.appendImageNotice("Usage: /image <path>  (image or PDF; or /image clear)")
	case strings.EqualFold(trimmed, "clear"):
		m.pendingImages = nil
		m.pendingImageLabels = nil
		m.pendingDocuments = nil
		return m.appendImageNotice("Cleared pending attachments.")
	}

	// A PDF carries a text layer every model can read, so it is not gated on
	// vision the way a raw image is; the optional rasterized pages are.
	if imageinput.IsProbablyDocumentPath(trimmed) {
		return m.handleDocumentAttach(trimmed)
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

// pendingDocument is a PDF staged by /image for the next user turn: its extracted
// text layer (prepended to the prompt at submit time) and a display label.
type pendingDocument struct {
	label string
	text  string
}

// handleDocumentAttach loads a PDF through imageinput.LoadDocument. The text
// layer is staged for every model; when the active model supports vision and a
// rasterizer is available, the rendered pages are staged through the existing
// pending-image pipeline too. A scanned PDF with no text (and no rasterizer)
// surfaces LoadDocument's explicit "no extractable text" notice and attaches
// nothing.
func (m model) handleDocumentAttach(path string) model {
	doc, err := imageinput.LoadDocument(path, m.cwd, imageinput.DocumentOptions{
		Vision: modelSupportsVisionTUI(m.modelName),
	})
	if err != nil {
		return m.appendImageNotice(err.Error())
	}

	label := filepath.Base(path)
	parts := make([]string, 0, 2)
	if strings.TrimSpace(doc.Text) != "" {
		m.pendingDocuments = append(m.pendingDocuments, pendingDocument{label: label, text: doc.Text})
		summary := "text"
		if doc.Truncated {
			summary = "text, truncated at the size limit"
		}
		parts = append(parts, summary)
	}
	for _, block := range doc.Images {
		m.pendingImages = append(m.pendingImages, block)
		m.pendingImageLabels = append(m.pendingImageLabels, label)
	}
	if len(doc.Images) > 0 {
		parts = append(parts, fmt.Sprintf("%d page image(s)", len(doc.Images)))
	}

	return m.appendImageNotice("Attached " + label + " (" + strings.Join(parts, ", ") + ").")
}

// consumePendingDocuments returns the staged document text formatted as a prompt
// preamble and clears the pending documents. The preamble names each document so
// the model can attribute the text; an empty result means nothing was staged.
func (m *model) consumePendingDocuments() string {
	if len(m.pendingDocuments) == 0 {
		return ""
	}
	var b strings.Builder
	for _, doc := range m.pendingDocuments {
		b.WriteString("Attached document: ")
		b.WriteString(doc.label)
		b.WriteString("\n")
		b.WriteString(doc.text)
		b.WriteString("\n\n")
	}
	m.pendingDocuments = nil
	return b.String()
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

// renderAttachmentChips builds the pending-attachment row from both staged
// images and staged documents, e.g. "[img: a.png] [doc: spec.pdf]". Returns ""
// when nothing is staged. Document chips are de-duplicated by label so a PDF
// that produced both a text block and several page images shows one "[doc: …]"
// rather than one per page (its page images already show as "[img: …]").
func renderAttachmentChips(imageLabels []string, docs []pendingDocument) string {
	chips := make([]string, 0, len(imageLabels)+len(docs))
	for _, label := range imageLabels {
		chips = append(chips, "[img: "+label+"]")
	}
	for _, doc := range docs {
		chips = append(chips, "[doc: "+doc.label+"]")
	}
	return strings.Join(chips, " ")
}
