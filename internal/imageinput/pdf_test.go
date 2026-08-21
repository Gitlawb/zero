package imageinput

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const minimalPDFTextChunkSize = 80

// buildMinimalPDF assembles a tiny, single-page PDF whose content stream draws
// the given text. It computes a real cross-reference table and trailer, keeping
// the repo free of opaque binary blobs for PDF routing tests.
func buildMinimalPDF(text string) []byte {
	var buf bytes.Buffer
	offsets := make([]int, 0, 8)
	startObj := func() { offsets = append(offsets, buf.Len()) }

	buf.WriteString("%PDF-1.4\n")

	startObj() // object 1: catalog
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	startObj() // object 2: page tree
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")

	pageHeight := minimalPDFPageHeight(text)
	startObj() // object 3: page
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 " + strconv.Itoa(pageHeight) + "] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>\nendobj\n")

	content := minimalPDFTextContent(text, pageHeight-92)
	startObj() // object 4: content stream
	buf.WriteString("4 0 obj\n<< /Length " + strconv.Itoa(len(content)) + " >>\nstream\n")
	buf.WriteString(content)
	buf.WriteString("\nendstream\nendobj\n")

	startObj() // object 5: font
	buf.WriteString("5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")

	xrefStart := buf.Len()
	buf.WriteString("xref\n")
	buf.WriteString("0 " + strconv.Itoa(len(offsets)+1) + "\n")
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	buf.WriteString("trailer\n<< /Size " + strconv.Itoa(len(offsets)+1) + " /Root 1 0 R >>\n")
	buf.WriteString("startxref\n" + strconv.Itoa(xrefStart) + "\n%%EOF\n")
	return buf.Bytes()
}

func minimalPDFPageHeight(text string) int {
	lines := (len(text) + minimalPDFTextChunkSize - 1) / minimalPDFTextChunkSize
	if lines < 1 {
		lines = 1
	}
	height := 184 + lines*10
	if height < 792 {
		return 792
	}
	return height
}

func minimalPDFTextContent(text string, startY int) string {
	var content strings.Builder
	content.WriteString("BT /F1 8 Tf 10 TL 72 ")
	content.WriteString(strconv.Itoa(startY))
	content.WriteString(" Td ")
	for index := 0; len(text) > 0; index++ {
		if index > 0 {
			content.WriteString(" T* ")
		}
		chunk := text
		if len(chunk) > minimalPDFTextChunkSize {
			cut := minimalPDFTextChunkSize
			for cut > 0 && !utf8RuneStart(chunk[cut]) {
				cut--
			}
			if cut == 0 {
				cut = minimalPDFTextChunkSize
			}
			chunk = text[:cut]
		}
		content.WriteString("(")
		content.WriteString(escapePDFLiteral(chunk))
		content.WriteString(") Tj")
		text = text[len(chunk):]
	}
	content.WriteString(" ET")
	return content.String()
}

func escapePDFLiteral(text string) string {
	text = strings.ReplaceAll(text, `\`, `\\`)
	text = strings.ReplaceAll(text, `(`, `\(`)
	text = strings.ReplaceAll(text, `)`, `\)`)
	text = strings.ReplaceAll(text, "\r", `\r`)
	text = strings.ReplaceAll(text, "\n", `\n`)
	return text
}

func TestIsPDF(t *testing.T) {
	if !isPDF(buildMinimalPDF("hi")) {
		t.Fatal("isPDF should accept real %PDF- bytes")
	}
	if !isPDF([]byte("%PDF-1.7\n...")) {
		t.Fatal("isPDF should accept a bare %PDF- magic prefix")
	}
	if isPDF([]byte("not a pdf at all")) {
		t.Fatal("isPDF should reject non-PDF bytes")
	}
	if isPDF(nil) {
		t.Fatal("isPDF should reject empty input")
	}
	if isPDF([]byte("%PDF")) {
		t.Fatal("isPDF should require the trailing dash of %PDF-")
	}
}

func TestLoadDocumentTextExtraction(t *testing.T) {
	root := t.TempDir()
	want := "Hello ZERO PDF"
	if err := os.WriteFile(filepath.Join(root, "doc.pdf"), buildMinimalPDF(want), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	stubPDFTools(t, want, false, 1)

	doc, err := LoadDocument("doc.pdf", root, DocumentOptions{})
	if err != nil {
		t.Fatalf("LoadDocument: %v", err)
	}
	if !strings.Contains(doc.Text, want) {
		t.Fatalf("extracted text %q should contain %q", doc.Text, want)
	}
	if len(doc.Images) != 0 {
		t.Fatalf("text-only extraction should not return images, got %d", len(doc.Images))
	}
	if doc.Pages != 1 {
		t.Fatalf("Pages = %d, want 1", doc.Pages)
	}
}

func TestExtractTextWithPoppler(t *testing.T) {
	originalLookup, originalCommand := popplerLookup, popplerCommandWithContext
	popplerLookup = func(name string) bool { return name == "pdftotext" }
	popplerCommandWithContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], "-test.run=TestPDFCommandHelper", "--")
	}
	t.Cleanup(func() {
		popplerLookup, popplerCommandWithContext = originalLookup, originalCommand
	})

	result := extractTextWithPoppler(buildMinimalPDF("ignored by helper"))
	if result.status != popplerTextFailed {
		t.Fatalf("status = %d, want execution failure", result.status)
	}
}

func TestPDFCommandHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "--" {
		return
	}
	os.Exit(1)
}

// A .pdf-named file that is not actually a PDF must be rejected with a clear
// error rather than silently treated as a document (extension is never trusted
// over magic bytes).
func TestLoadDocumentRejectsFakePDF(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fake.pdf"), []byte("this is plainly not a PDF document"), 0o644); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	_, err := LoadDocument("fake.pdf", root, DocumentOptions{})
	if err == nil {
		t.Fatal("expected error for a .pdf-named non-PDF file")
	}
	if !strings.Contains(err.Error(), "not a PDF") {
		t.Fatalf("error %q should explain the file is not a PDF", err.Error())
	}
}

func TestLoadDocumentMissing(t *testing.T) {
	root := t.TempDir()
	_, err := LoadDocument("nope.pdf", root, DocumentOptions{})
	if err == nil {
		t.Fatal("expected error for a missing file")
	}
	if !strings.Contains(err.Error(), "nope.pdf") {
		t.Fatalf("error %q should name the path", err.Error())
	}
}

func TestLoadDocumentRejectsNonRegular(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "adir.pdf"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := LoadDocument("adir.pdf", root, DocumentOptions{})
	if err == nil {
		t.Fatal("expected error for a non-regular file")
	}
	if !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("error %q should mention regular file", err.Error())
	}
}

// The per-document byte cap is enforced before parsing, mirroring the image cap,
// so an unbounded file never reaches the parser or a provider.
func TestLoadDocumentOversizeRejected(t *testing.T) {
	root := t.TempDir()
	big := make([]byte, MaxDocumentBytes+1)
	copy(big, []byte("%PDF-1.4\n"))
	if err := os.WriteFile(filepath.Join(root, "big.pdf"), big, 0o644); err != nil {
		t.Fatalf("write big: %v", err)
	}
	_, err := LoadDocument("big.pdf", root, DocumentOptions{})
	if err == nil {
		t.Fatal("expected error for an oversize document")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Fatalf("error %q should mention the size limit", err.Error())
	}
}

// Extracted text longer than the cap is truncated (with a marker) rather than
// rejected: a large but valid spec/doc should still be partially usable.
func TestLoadDocumentTruncatesLongText(t *testing.T) {
	root := t.TempDir()
	// Many short lines so the *extracted text* (not the file) exceeds the cap.
	var body strings.Builder
	line := "The quick brown fox jumps over the lazy dog. "
	for body.Len() < MaxDocumentTextBytes+65536 {
		body.WriteString(line)
	}
	if err := os.WriteFile(filepath.Join(root, "long.pdf"), buildMinimalPDF(body.String()), 0o644); err != nil {
		t.Fatalf("write long: %v", err)
	}
	stubPDFTools(t, body.String(), true, 1)
	doc, err := LoadDocument("long.pdf", root, DocumentOptions{})
	if err != nil {
		t.Fatalf("LoadDocument: %v", err)
	}
	if len(doc.Text) > MaxDocumentTextBytes {
		t.Fatalf("capped text length %d exceeds cap %d (marker must be counted against the cap)", len(doc.Text), MaxDocumentTextBytes)
	}
	if !doc.Truncated {
		t.Fatalf("Truncated should be set when extracted text is capped; extracted len=%d pages=%d", len(doc.Text), doc.Pages)
	}
	if !strings.Contains(doc.Text, documentTruncatedMarker) {
		t.Fatal("truncated text should carry the truncation marker")
	}
}

// A PDF with no extractable text layer and no rasterization available must
// surface an explicit error, never a silent empty success.
func TestLoadDocumentNoTextNoRaster(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "scan.pdf"), buildEmptyTextPDF(), 0o644); err != nil {
		t.Fatalf("write scan: %v", err)
	}
	// Simulate a host without Poppler so the no-text branch is deterministic.
	_, err := LoadDocument("scan.pdf", root, DocumentOptions{disableExternalTools: true})
	if err == nil {
		t.Fatal("expected an error for a PDF with no extractable text and no raster")
	}
	if !strings.Contains(err.Error(), "no extractable text") {
		t.Fatalf("error %q should explain that no text is available", err.Error())
	}
}

// buildEmptyTextPDF is a valid single-page PDF with an empty content stream:
// structurally a PDF, but with no text layer to extract.
func buildEmptyTextPDF() []byte {
	var buf bytes.Buffer
	offsets := make([]int, 0, 8)
	startObj := func() { offsets = append(offsets, buf.Len()) }

	buf.WriteString("%PDF-1.4\n")
	startObj()
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	startObj()
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	startObj()
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << >> /Contents 4 0 R >>\nendobj\n")
	startObj()
	buf.WriteString("4 0 obj\n<< /Length 0 >>\nstream\n\nendstream\nendobj\n")

	xrefStart := buf.Len()
	buf.WriteString("xref\n")
	buf.WriteString("0 " + strconv.Itoa(len(offsets)+1) + "\n")
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	buf.WriteString("trailer\n<< /Size " + strconv.Itoa(len(offsets)+1) + " /Root 1 0 R >>\n")
	buf.WriteString("startxref\n" + strconv.Itoa(xrefStart) + "\n%%EOF\n")
	return buf.Bytes()
}

// Malformed PDF bytes that pass the header check must produce a clean error
// when no safe extractor is available.
func TestLoadDocumentMalformedDoesNotPanic(t *testing.T) {
	root := t.TempDir()
	bad := []byte("%PDF-1.4\nthis header is valid but the body and xref are garbage\nstartxref\n9\n%%EOF\n")
	if err := os.WriteFile(filepath.Join(root, "bad.pdf"), bad, 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	_, err := LoadDocument("bad.pdf", root, DocumentOptions{disableExternalTools: true})
	if err == nil {
		t.Fatal("expected an error for malformed PDF bytes")
	}
}

func TestLoadDocumentRequiresBoundedExtractor(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "doc.pdf"), buildMinimalPDF("text"), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	_, err := LoadDocument("doc.pdf", root, DocumentOptions{disableExternalTools: true})
	if err == nil || !strings.Contains(err.Error(), "pdftotext") {
		t.Fatalf("LoadDocument error = %v, want bounded-extractor guidance", err)
	}
}

func TestLoadDocumentDoesNotMisreportInstalledPopplerFailure(t *testing.T) {
	root := t.TempDir()
	bad := []byte("%PDF-1.4\nthis header is valid but the body and xref are garbage\nstartxref\n9\n%%EOF\n")
	if err := os.WriteFile(filepath.Join(root, "bad.pdf"), bad, 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	original := popplerTextExtractor
	popplerTextExtractor = func([]byte) popplerTextResult { return popplerTextResult{status: popplerTextFailed} }
	t.Cleanup(func() { popplerTextExtractor = original })

	_, err := LoadDocument("bad.pdf", root, DocumentOptions{})
	if err == nil || !strings.Contains(err.Error(), "could not extract PDF text") {
		t.Fatalf("LoadDocument error = %v, want extraction failure", err)
	}
	if strings.Contains(err.Error(), "install Poppler") {
		t.Fatalf("LoadDocument error = %q must not claim Poppler is absent", err)
	}
}

func TestLoadDocumentVisionUsesText(t *testing.T) {
	root := t.TempDir()
	want := "Vision degrade to text"
	if err := os.WriteFile(filepath.Join(root, "doc.pdf"), buildMinimalPDF(want), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	stubPDFTools(t, want, false, 1)

	doc, err := LoadDocument("doc.pdf", root, DocumentOptions{Vision: true})
	if err != nil {
		t.Fatalf("LoadDocument: %v", err)
	}
	if !strings.Contains(doc.Text, want) {
		t.Fatalf("vision input should keep text, got %q", doc.Text)
	}
}

func stubPDFTools(t *testing.T, text string, overflow bool, pages int) {
	t.Helper()
	originalTextExtractor, originalPageCounter := popplerTextExtractor, popplerPageCounter
	popplerTextExtractor = func([]byte) popplerTextResult {
		return popplerTextResult{text: text, overflow: overflow, status: popplerTextExtracted}
	}
	popplerPageCounter = func([]byte) int { return pages }
	t.Cleanup(func() {
		popplerTextExtractor, popplerPageCounter = originalTextExtractor, originalPageCounter
	})
}

// capDocumentText must keep the final payload (text + marker) at or under the
// advertised cap: the marker is counted against MaxDocumentTextBytes, not added
// on top of it.
func TestCapDocumentTextRespectsCap(t *testing.T) {
	// Text comfortably over the cap so truncation triggers.
	over := strings.Repeat("a", MaxDocumentTextBytes+1024)
	got, truncated := capDocumentText(over)
	if !truncated {
		t.Fatal("expected truncation for over-cap text")
	}
	if len(got) > MaxDocumentTextBytes {
		t.Fatalf("capped text length %d exceeds cap %d", len(got), MaxDocumentTextBytes)
	}
	if !strings.HasSuffix(got, documentTruncatedMarker) {
		t.Fatal("capped text should end with the truncation marker")
	}

	// At-or-under the cap is returned unchanged with no marker.
	under := strings.Repeat("b", MaxDocumentTextBytes)
	got, truncated = capDocumentText(under)
	if truncated {
		t.Fatal("text exactly at the cap must not be truncated")
	}
	if got != under {
		t.Fatal("at-cap text must be returned unchanged")
	}

	got, truncated = capDocumentTextWithOverflow(under, true)
	if !truncated {
		t.Fatal("upstream overflow must preserve truncation after whitespace trimming")
	}
	if !strings.HasSuffix(got, documentTruncatedMarker) {
		t.Fatal("upstream overflow should add the truncation marker")
	}

	got, truncated = capDocumentTextWithOverflow("x", true)
	if !truncated || got != "x"+documentTruncatedMarker {
		t.Fatalf("short overflow = (%q, %v), want text plus marker without a panic", got, truncated)
	}
}

func TestPDFOutputReadersAreBounded(t *testing.T) {
	buffer := newBoundedBuffer(16)
	if _, err := buffer.Write([]byte(strings.Repeat("y", 1024))); err != nil {
		t.Fatalf("boundedBuffer.Write: %v", err)
	}
	if !buffer.overflow {
		t.Fatal("boundedBuffer should report overflow")
	}
	if buffer.Len() != 17 {
		t.Fatalf("boundedBuffer retained %d bytes, want 17", buffer.Len())
	}

	buffer = newBoundedBuffer(16)
	_, _ = buffer.Write([]byte(strings.Repeat("z", 17)))
	if !buffer.overflow {
		t.Fatal("boundedBuffer must report exactly limit+1 bytes as overflow")
	}
}

func TestResolvePageCountUsesPdfinfoWhenAvailable(t *testing.T) {
	original := popplerPageCounter
	popplerPageCounter = func([]byte) int { return 7 }
	t.Cleanup(func() { popplerPageCounter = original })

	if got := resolvePageCount(nil, true, 0); got != 7 {
		t.Fatalf("pdfinfo count = %d, want 7", got)
	}
	if got := resolvePageCount(nil, false, 0); got != 0 {
		t.Fatalf("external tools disabled: Pages = %d, want 0", got)
	}
	if got := resolvePageCount(nil, true, 3); got != 3 {
		t.Fatalf("already-known count: Pages = %d, want 3", got)
	}
}

func TestLoadDocumentHostilePDFDoesNotUseInProcessParser(t *testing.T) {
	root := t.TempDir()
	cases := map[string][]byte{
		"cycle.pdf": []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 1 0 R /Parent 1 0 R /Kids [1 0 R] /Count 999999999 /First 1 0 R /Next 1 0 R >>\nendobj\ntrailer\n<< /Root 1 0 R /Size 999999999 >>\nstartxref\n9\n%%EOF\n"),
		"hex.pdf":   []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\nstream\n<" + strings.Repeat("A", 4096) + "\nendstream\n%%EOF\n"),
	}
	for name, body := range cases {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		_, err := LoadDocument(name, root, DocumentOptions{disableExternalTools: true})
		if err == nil || !strings.Contains(err.Error(), "pdftotext") {
			t.Fatalf("LoadDocument(%s) error = %v, want bounded-extractor guidance", name, err)
		}
	}
}

// LooksLikeDocumentFile sniffs PDF content by magic bytes, so a real PDF with no
// ".pdf" extension is still recognized while a non-PDF (even named .pdf) is not.
func TestLooksLikeDocumentFile(t *testing.T) {
	root := t.TempDir()
	// A real PDF named without a .pdf extension.
	if err := os.WriteFile(filepath.Join(root, "spec"), buildMinimalPDF("hi"), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	// A non-PDF named with a .pdf extension.
	if err := os.WriteFile(filepath.Join(root, "fake.pdf"), []byte("not a pdf"), 0o644); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	// A directory must not be treated as a document.
	if err := os.Mkdir(filepath.Join(root, "adir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if !LooksLikeDocumentFile("spec", root) {
		t.Fatal("a real PDF without a .pdf extension should be recognized by content")
	}
	if LooksLikeDocumentFile("fake.pdf", root) {
		t.Fatal("a .pdf-named non-PDF must not be recognized as a document")
	}
	if LooksLikeDocumentFile("nope", root) {
		t.Fatal("a missing file must not be recognized as a document")
	}
	if LooksLikeDocumentFile("adir", root) {
		t.Fatal("a directory must not be recognized as a document")
	}
}

func TestIsProbablyDocumentPath(t *testing.T) {
	cases := map[string]bool{
		"report.pdf":      true,
		"REPORT.PDF":      true,
		"a/b/spec.Pdf":    true,
		"image.png":       false,
		"notes.txt":       false,
		"noext":           false,
		"archive.pdf.zip": false,
	}
	for path, want := range cases {
		if got := IsProbablyDocumentPath(path); got != want {
			t.Fatalf("IsProbablyDocumentPath(%q) = %v, want %v", path, got, want)
		}
	}
}
