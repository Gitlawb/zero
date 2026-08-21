package imageinput

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// Dependency posture: PDF text extraction uses Poppler's pdftotext when it is
// on PATH and disableExternalTools is false. We intentionally do not retain an
// in-process parser fallback: the previously used parser materialized all
// decompressed page text in Zero's own process before exposing a reader. Poppler
// runs in a separately cancellable process with a capped captured output and a
// fixed deadline. Rasterizing pages to images for vision models needs
// real font/graphics rendering and uses pdftoppm only when it is already on
// PATH -- the same "external tool the user may have" posture as the LSP
// language servers. When Poppler is unavailable, text extraction fails clearly
// instead of processing an untrusted document without enforceable limits.

// MaxDocumentBytes is the per-document raw-file cap (32 MiB). PDFs are routinely
// larger than the image cap, but we still bound the file before it is read into
// memory or handed to a parser so an unbounded file never reaches a provider.
const MaxDocumentBytes = 32 << 20

// MaxDocumentTextBytes caps the EXTRACTED text we hand to a model (256 KiB).
// Unlike the raw-file cap (which rejects), an over-cap text layer is truncated
// with documentTruncatedMarker so a large-but-valid spec is still partially
// usable instead of refused outright.
const MaxDocumentTextBytes = 256 << 10

// maxPDFInfoOutputBytes bounds the small metadata response consumed from
// pdfinfo. It is intentionally separate from the text cap because page-count
// output is not exposed to the model.
const maxPDFInfoOutputBytes = 64 << 10

// documentTruncatedMarker is appended to capped text so the agent (and the user)
// can tell extraction was cut short rather than the document simply ending.
const documentTruncatedMarker = "\n\n[... document text truncated at the size limit ...]"

// defaultMaxRasterPages bounds how many pages the optional vision path renders,
// so a long PDF cannot blow up the context window. Callers may override it via
// DocumentOptions.MaxPages.
const defaultMaxRasterPages = 10

// maxRasterDimension caps both dimensions passed to pdftoppm. The resulting
// bitmap is below the per-image byte cap even before PNG compression, preventing
// a tiny PDF with an enormous media box from filling temporary storage.
const maxRasterDimension = 1536

// popplerTimeout bounds the whole Poppler phase of one PDF attachment so a
// wedged or pathological document cannot multiply the synchronous CLI/TUI wait
// across rasterization, text extraction, and page counting.
const popplerTimeout = 30 * time.Second

// rasterTimeout bounds optional page rendering independently. It lets a vision
// attachment retain useful diagrams/layout without letting rendering outlive the
// user-facing extraction deadline or multiply it serially.
const rasterTimeout = 10 * time.Second

// pdfMagic is the leading signature of every PDF stream. Detection keys on these
// bytes, never on the file extension alone.
var pdfMagic = []byte("%PDF-")

// Document is the result of ingesting a PDF: its extracted text layer when the
// bounded extractor succeeds plus, on the optional vision path, one ImageBlock
// per rendered page. Pages is best-effort external metadata and may be zero;
// Truncated is set when Text was capped at MaxDocumentTextBytes.
type Document struct {
	Text      string
	Images    []zeroruntime.ImageBlock
	Pages     int
	Truncated bool
}

// DocumentOptions tunes how a PDF is ingested.
type DocumentOptions struct {
	// Vision asks for page rasterization (ImageBlocks) in addition to text, for a
	// vision-capable model. It is best-effort: when no rasterizer is available the
	// load degrades to the text layer rather than erroring.
	Vision bool
	// MaxPages bounds how many pages the vision path renders. Zero means
	// defaultMaxRasterPages.
	MaxPages int

	// disableExternalTools simulates an unavailable Poppler installation for
	// deterministic tests. It is intentionally unexported and not public API.
	disableExternalTools bool
}

// isPDF reports whether data begins with the PDF magic signature ("%PDF-"). It
// is the authoritative check: the file extension is only a hint.
func isPDF(data []byte) bool {
	return bytes.HasPrefix(data, pdfMagic)
}

// IsProbablyDocumentPath reports whether a path looks like a document ZERO can
// ingest (currently: a ".pdf" extension, case-insensitive). It is only a routing
// hint for input surfaces deciding whether to call LoadDocument vs LoadFile;
// LoadDocument re-verifies the real content via magic bytes.
func IsProbablyDocumentPath(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".pdf")
}

// LooksLikeDocumentFile reports whether the file at path is a PDF by content,
// reading only its leading magic bytes. It lets input surfaces route a real PDF
// to LoadDocument even when its name lacks a ".pdf" extension, so detection is
// content-based rather than extension-only. It opens nothing it cannot stat as a
// regular file and never reads more than the magic prefix; any I/O error (missing
// file, permission, non-regular) simply reports false and lets the normal path
// surface a precise error. Relative paths resolve against workspaceRoot.
func LooksLikeDocumentFile(path string, workspaceRoot string) bool {
	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(workspaceRoot, resolved)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	file, err := os.Open(resolved)
	if err != nil {
		return false
	}
	defer file.Close()

	header := make([]byte, len(pdfMagic))
	n, err := io.ReadFull(file, header)
	if err != nil && n < len(pdfMagic) {
		return false
	}
	return isPDF(header)
}

// LoadDocument reads the PDF at path (resolved against workspaceRoot when
// relative), enforces the per-document size cap, and extracts its text layer.
// With opts.Vision and an available rasterizer it also renders the first N pages
// to ImageBlocks. The file is identified by magic bytes, not its extension, so a
// ".pdf"-named non-PDF is rejected with a clear error. A PDF with no text layer
// and no rasterization/OCR available returns an explicit error rather than a
// silent empty success. Errors are plain (callers wrap them into surface-specific notice text).
func LoadDocument(path string, workspaceRoot string, opts DocumentOptions) (Document, error) {
	data, err := readDocumentBytes(path, workspaceRoot)
	if err != nil {
		return Document{}, err
	}
	if !isPDF(data) {
		return Document{}, fmt.Errorf("%s is not a PDF (expected a %%PDF- file)", path)
	}

	useExternal := !opts.disableExternalTools

	// Start the independent Poppler operations under one deadline. LoadDocument
	// runs on the synchronous /image path; running these serially would let one
	// hostile PDF spend a separate timeout in each process.
	var images []zeroruntime.ImageBlock
	textResult := popplerTextResult{status: popplerTextUnavailable}
	pages := 0
	if useExternal {
		ctx, cancel := context.WithTimeout(context.Background(), popplerOperationTimeout)
		var work sync.WaitGroup
		textDone := make(chan struct{})
		var rasterDone <-chan struct{}
		work.Add(2)
		go func() {
			defer work.Done()
			defer close(textDone)
			textResult = popplerTextExtractor(ctx, data)
		}()
		go func() {
			defer work.Done()
			pages = popplerPageCounter(ctx, data)
		}()
		if opts.Vision {
			done := make(chan struct{})
			rasterDone = done
			work.Add(1)
			go func() {
				defer work.Done()
				defer close(done)
				rasterCtx, rasterCancel := context.WithTimeout(context.Background(), rasterTimeout)
				defer rasterCancel()
				// Rendering is optional: text remains usable if it fails or times out.
				if rendered, rerr := popplerRasterizer(rasterCtx, data, opts.maxPages()); rerr == nil {
					images = rendered
				}
			}()
		}
		<-textDone
		// Page count is informational. Rendering is optional but, when requested,
		// contributes usable vision input and has its own shorter deadline.
		if rasterDone != nil {
			<-rasterDone
		}
		cancel()
		work.Wait()
	}

	// Text path. Poppler output is retained through a bounded writer. There is no
	// in-process fallback because its parser cannot enforce this boundary before
	// decompression and text aggregation.
	text, textOverflow := "", false
	textStatus := textResult.status
	if textStatus == popplerTextExtracted {
		text, textOverflow = textResult.text, textResult.overflow
	}

	// Decide whether any usable text exists before adding a truncation marker.
	// Otherwise whitespace-only overflow could turn into a marker-only document
	// that bypasses the no-text guard below.
	hasText := strings.TrimSpace(text) != ""
	if !hasText {
		text, textOverflow = "", false
	}
	text, truncated := capDocumentTextWithOverflow(text, textOverflow)

	// Scanned-PDF guard: no text layer AND no rendered pages means we have nothing
	// the model can use. Say so explicitly instead of returning empty success.
	if !hasText && len(images) == 0 {
		if textStatus == popplerTextFailed {
			return Document{}, fmt.Errorf("%s could not extract PDF text with pdftotext", path)
		}
		if textStatus == popplerTextUnavailable {
			return Document{}, fmt.Errorf("%s has no extractable text; install Poppler's pdftotext for PDF text extraction (and pdftoppm for image-only PDFs)", path)
		}
		return Document{}, fmt.Errorf("%s has no extractable text; PDF OCR is not available", path)
	}
	return Document{Text: text, Images: images, Pages: pages, Truncated: truncated}, nil
}

// readDocumentBytes resolves path against workspaceRoot, rejects missing,
// non-regular, and oversized files (mirroring LoadFile), and returns the raw
// bytes with a hard bound so an unbounded source can never allocate without
// limit.
func readDocumentBytes(path string, workspaceRoot string) ([]byte, error) {
	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(workspaceRoot, resolved)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("document file not found: %s", path)
	}
	// Reject non-regular files (directories, FIFOs, devices) before os.Open so a
	// writerless FIFO can never block the read forever -- same guard as LoadFile.
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("document file must be a regular file: %s", path)
	}
	if info.Size() > MaxDocumentBytes {
		return nil, fmt.Errorf("document %s is larger than the 32 MiB limit", path)
	}

	file, err := os.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("cannot open document %s: %w", path, err)
	}
	defer file.Close()

	// LimitReader of cap+1: at most one byte past the cap is buffered, so the cap
	// is the real bound regardless of any stat/read race or a growing file.
	data, err := io.ReadAll(io.LimitReader(file, MaxDocumentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read document %s: %w", path, err)
	}
	if len(data) > MaxDocumentBytes {
		return nil, fmt.Errorf("document %s is larger than the 32 MiB limit", path)
	}
	return data, nil
}

// capDocumentText truncates text to MaxDocumentTextBytes on a UTF-8 rune
// boundary and appends documentTruncatedMarker when it had to cut. The second
// return reports whether truncation happened. The marker is counted against the
// cap so the returned string never exceeds MaxDocumentTextBytes.
func capDocumentText(text string) (string, bool) {
	return capDocumentTextWithOverflow(text, false)
}

// capDocumentTextWithOverflow applies the model text cap and preserves a
// truncation signal from a bounded upstream reader. That signal is necessary
// when trimming whitespace makes the retained string appear to fit the cap.
func capDocumentTextWithOverflow(text string, overflow bool) (string, bool) {
	if !overflow && len(text) <= MaxDocumentTextBytes {
		return text, false
	}
	// Reserve room for the marker so the final payload (text + marker) stays at or
	// under the advertised cap. If the marker alone would not fit, cut to nothing
	// and return just the marker.
	cut := MaxDocumentTextBytes - len(documentTruncatedMarker)
	if cut < 0 {
		cut = 0
	}
	if cut > len(text) {
		cut = len(text)
	}
	// Back up to a rune boundary so we never split a multi-byte character.
	for cut > 0 && cut < len(text) && !utf8RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + documentTruncatedMarker, true
}

// utf8RuneStart reports whether b can start a UTF-8 rune (i.e. it is not a
// continuation byte 0b10xxxxxx). Kept local to avoid pulling in unicode/utf8
// for a single bit test.
func utf8RuneStart(b byte) bool {
	return b&0xC0 != 0x80
}

type popplerTextStatus uint8

const (
	popplerTextUnavailable popplerTextStatus = iota
	popplerTextFailed
	popplerTextExtracted
)

type popplerTextResult struct {
	text     string
	overflow bool
	status   popplerTextStatus
}

var (
	popplerTextExtractor      = extractTextWithPoppler
	popplerPageCounter        = pdfPageCountWithPoppler
	popplerRasterizer         = rasterizeWithPoppler
	popplerLookup             = popplerAvailable
	popplerCommandWithContext = exec.CommandContext
	popplerOperationTimeout   = popplerTimeout
)

func (o DocumentOptions) maxPages() int {
	if o.MaxPages > 0 && o.MaxPages < defaultMaxRasterPages {
		return o.MaxPages
	}
	return defaultMaxRasterPages
}

// --- Optional external poppler path -----------------------------------------

// popplerAvailable reports whether a poppler binary is resolvable on PATH.
func popplerAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// extractTextWithPoppler runs `pdftotext - -` (read stdin, write stdout). It
// keeps executable discovery distinct from execution failure so callers can
// provide accurate, non-sensitive remediation without exposing tool stderr.
func extractTextWithPoppler(ctx context.Context, data []byte) popplerTextResult {
	if !popplerLookup("pdftotext") {
		return popplerTextResult{status: popplerTextUnavailable}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// "-layout" keeps the visual column layout; the trailing "- -" reads the PDF
	// from stdin and writes UTF-8 text to stdout.
	cmd := popplerCommandWithContext(ctx, "pdftotext", "-layout", "-enc", "UTF-8", "-", "-")
	cmd.Stdin = bytes.NewReader(data)
	stdout := newBoundedBuffer(MaxDocumentTextBytes)
	stdout.onOverflow = cancel
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if stdout.overflow {
			return popplerTextResult{text: strings.TrimSpace(stdout.String()), overflow: true, status: popplerTextExtracted}
		}
		return popplerTextResult{status: popplerTextFailed}
	}
	return popplerTextResult{text: strings.TrimSpace(stdout.String()), overflow: stdout.overflow, status: popplerTextExtracted}
}

func pdfPageCountWithPoppler(ctx context.Context, data []byte) int {
	if !popplerAvailable("pdfinfo") {
		return 0
	}
	cmd := exec.CommandContext(ctx, "pdfinfo", "-")
	cmd.Stdin = bytes.NewReader(data)
	var out boundedBuffer
	out.limit = maxPDFInfoOutputBytes
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil || out.overflow {
		return 0
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "Pages:"); ok {
			var pages int
			if _, err := fmt.Sscan(value, &pages); err == nil {
				return pages
			}
		}
	}
	return 0
}

// boundedBuffer retains at most limit+1 bytes while accepting the complete
// write. The extra byte distinguishes exact-limit output from overflow without
// allowing a subprocess or parser to grow memory without bound.
type boundedBuffer struct {
	buffer     bytes.Buffer
	limit      int
	overflow   bool
	onOverflow func()
}

func newBoundedBuffer(limit int) boundedBuffer {
	return boundedBuffer{limit: limit}
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	remaining := buffer.limit + 1 - buffer.buffer.Len()
	if remaining > 0 {
		if remaining > len(data) {
			remaining = len(data)
		}
		_, _ = buffer.buffer.Write(data[:remaining])
	}
	if buffer.buffer.Len() > buffer.limit {
		if !buffer.overflow {
			buffer.overflow = true
			if buffer.onOverflow != nil {
				buffer.onOverflow()
			}
		}
	}
	return len(data), nil
}

func (buffer *boundedBuffer) Len() int { return buffer.buffer.Len() }

func (buffer *boundedBuffer) String() string { return buffer.buffer.String() }

// rasterizeWithPoppler renders the first maxPages pages to PNG via pdftoppm and
// returns them as normalized ImageBlocks (reusing the image allow-list, sniff,
// and per-image cap). It returns an error when pdftoppm is absent or rendering
// produced nothing; the caller treats that as "no rasterization available" and
// keeps the text layer.
func rasterizeWithPoppler(ctx context.Context, data []byte, maxPages int) ([]zeroruntime.ImageBlock, error) {
	if !popplerAvailable("pdftoppm") {
		return nil, fmt.Errorf("pdftoppm not available")
	}
	if maxPages <= 0 {
		maxPages = defaultMaxRasterPages
	}

	dir, err := os.MkdirTemp("", "zero-pdf-raster-")
	if err != nil {
		return nil, fmt.Errorf("cannot create temp dir for rasterization: %w", err)
	}
	defer os.RemoveAll(dir)

	prefix := filepath.Join(dir, "page")
	// -png: PNG output; -r 150: legible default resolution; -scale-to limits
	// each output bitmap's largest dimension; -f 1 / -l N limits page count.
	cmd := exec.CommandContext(ctx, "pdftoppm", "-png", "-r", "150", "-scale-to", fmt.Sprintf("%d", maxRasterDimension), "-f", "1", "-l", fmt.Sprintf("%d", maxPages), "-", prefix)
	cmd.Stdin = bytes.NewReader(data)
	// Renderer diagnostics are not surfaced to callers; retaining hostile tool
	// output would bypass the attachment's bounded-output contract.
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pdftoppm failed: %w", err)
	}

	entries, err := filepath.Glob(prefix + "*.png")
	if err != nil {
		return nil, fmt.Errorf("cannot list rendered pages: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("rasterization produced no pages")
	}
	// Glob order is lexical; pdftoppm zero-pads page numbers, so a numeric-aware
	// sort keeps page 10 after page 9 rather than after page 1.
	sort.Strings(entries)

	images := make([]zeroruntime.ImageBlock, 0, len(entries))
	for _, name := range entries {
		if len(images) >= maxPages {
			break
		}
		block, err := loadRenderedPage(name)
		if err != nil {
			// Skip a single unreadable page rather than failing the whole render.
			continue
		}
		images = append(images, block)
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("no rendered pages could be loaded")
	}
	return images, nil
}

// loadRenderedPage reads one rendered PNG page, enforces the per-image cap, and
// normalizes its media type through the same allow-list LoadFile uses, so
// rasterized pages flow through the existing image pipeline unchanged.
func loadRenderedPage(name string) (zeroruntime.ImageBlock, error) {
	info, err := os.Stat(name)
	if err != nil {
		return zeroruntime.ImageBlock{}, err
	}
	if info.Size() > MaxImageBytes {
		return zeroruntime.ImageBlock{}, fmt.Errorf("rendered page %s exceeds the per-image limit", filepath.Base(name))
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return zeroruntime.ImageBlock{}, err
	}
	if len(data) > MaxImageBytes {
		return zeroruntime.ImageBlock{}, fmt.Errorf("rendered page %s exceeds the per-image limit", filepath.Base(name))
	}
	sniffLen := len(data)
	if sniffLen > 512 {
		sniffLen = 512
	}
	mediaType := zeroruntime.NormalizeImageMediaType(http.DetectContentType(data[:sniffLen]))
	if mediaType == "" {
		return zeroruntime.ImageBlock{}, fmt.Errorf("rendered page %s has an unsupported image type", filepath.Base(name))
	}
	return zeroruntime.ImageBlock{MediaType: mediaType, Data: data}, nil
}
