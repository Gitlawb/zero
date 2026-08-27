// file_view.go is the drill-in view for a touched file, opened from the FILES
// sidebar section (files_panel.go). It reuses the subchat pattern: while
// active, the chat column's body swaps to this file's content — the sidebar,
// composer, and scroll engine keep working unchanged (transcriptBodyItems is
// the single source the viewport, renderer, and hit-testers all read, so
// swapping there keeps every consumer consistent). Two modes:
//
//	diff (default) — the file's edit cards from this session, full-depth
//	full           — the file as it stands on disk, syntax highlighted, with
//	                 gutter markers on the lines this session's diffs added
//
// d/f switch modes (with an empty composer), Esc returns to the chat at the
// scroll position it was left at.
package tui

import (
	"bufio"
	"container/list"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
)

// fileViewMaxLines caps the full-file mode so a giant generated file can't
// freeze a render; the tail collapses into a "… more lines (file truncated at N for display)" trailer.
const (
	fileViewMaxLines               = 4000
	fileViewMaxBytes               = 1 << 20 // 1 MiB total read budget per file
	fileViewMaxLineBytes           = 4096    // 4 KiB max line length budget
	defaultFileViewCacheMaxEntries = 64
	fileViewMaxRenderVariants      = 4 // max rendered variants (width/fingerprint) per cached file entry
	fileViewLoadingPlaceholder     = "Loading…"
)

const (
	fileViewDiff = iota
	fileViewFull
)

var (
	fileViewLifetimeTS  atomic.Uint64
	fileViewLifetimeSeq atomic.Uint32
)

// nextFileViewLifetimeToken produces a monotonic, time-ordered 128-bit UUIDv7 (RFC 9562)
// to uniquely identify the lifecycle of a file view session without heap allocations.
func nextFileViewLifetimeToken() [16]byte {
	nowMs := uint64(time.Now().UnixMilli())
	for {
		last := fileViewLifetimeTS.Load()
		if nowMs > last {
			if fileViewLifetimeTS.CompareAndSwap(last, nowMs) {
				fileViewLifetimeSeq.Store(0)
				break
			}
		} else {
			nowMs = last
			break
		}
	}
	seq := fileViewLifetimeSeq.Add(1)
	var u [16]byte
	u[0] = byte(nowMs >> 40)
	u[1] = byte(nowMs >> 32)
	u[2] = byte(nowMs >> 24)
	u[3] = byte(nowMs >> 16)
	u[4] = byte(nowMs >> 8)
	u[5] = byte(nowMs)
	u[6] = 0x70 | byte((seq>>8)&0x0F) // Version 7
	u[7] = byte(seq & 0xFF)
	u[8] = 0x80 | byte((seq>>16)&0x3F) // RFC 9562 variant
	u[9] = byte(seq >> 24)
	return u
}

// fileViewCacheStats tracks disk I/O, Chroma highlighting, and cache hits/misses.
type fileViewCacheStats struct {
	DiskReads       int
	HighlightCalls  int
	CacheHits       int
	CacheMisses     int
	Evictions       int
	ThemeClears     int
	RenderEvictions int
}

type fileViewCachedEntry struct {
	targetPath   string
	displayPath  string
	modTime      time.Time
	size         int64
	lines        []string
	display      []string
	truncated    bool
	omittedLines bool

	rendersMu  sync.RWMutex
	renderKeys []string          // LRU order: oldest at index 0, most recent at end
	renders    map[string]string // key: "width:changedLinesFingerprint" -> formatted ANSI string
}

func (e *fileViewCachedEntry) getRender(key string) (string, bool) {
	e.rendersMu.RLock()
	val, ok := e.renders[key]
	e.rendersMu.RUnlock()
	if !ok {
		return "", false
	}
	e.rendersMu.Lock()
	for i, k := range e.renderKeys {
		if k == key {
			e.renderKeys = append(append(e.renderKeys[:i], e.renderKeys[i+1:]...), key)
			break
		}
	}
	e.rendersMu.Unlock()
	return val, true
}

func (e *fileViewCachedEntry) putRender(key string, val string) {
	e.rendersMu.Lock()
	defer e.rendersMu.Unlock()
	if e.renders == nil {
		e.renders = make(map[string]string)
	}
	if _, ok := e.renders[key]; !ok {
		for len(e.renders) >= fileViewMaxRenderVariants {
			if len(e.renderKeys) > 0 {
				oldKey := e.renderKeys[0]
				e.renderKeys = e.renderKeys[1:]
				delete(e.renders, oldKey)
			} else {
				for k := range e.renders {
					delete(e.renders, k)
					break
				}
			}
		}
		e.renderKeys = append(e.renderKeys, key)
	}
	e.renders[key] = val
}

type fileViewRenderCache struct {
	mu         sync.Mutex
	maxEntries int
	items      map[string]*list.Element // targetPath -> *list.Element containing *fileViewCachedEntry
	lru        *list.List
	gen        int
	statsData  fileViewCacheStats
}

var defaultFileViewCache = newFileViewRenderCache(defaultFileViewCacheMaxEntries)

func newFileViewRenderCache(maxEntries int) *fileViewRenderCache {
	return &fileViewRenderCache{
		maxEntries: maxEntries,
		items:      make(map[string]*list.Element),
		lru:        list.New(),
	}
}

func (c *fileViewRenderCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.items = make(map[string]*list.Element)
	c.lru.Init()
	c.statsData.ThemeClears++
}

func (c *fileViewRenderCache) generation() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

func (c *fileViewRenderCache) resetStats() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statsData = fileViewCacheStats{}
}

func (c *fileViewRenderCache) stats() fileViewCacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statsData
}

type fileViewReadResult struct {
	lines        []string
	truncated    bool
	omittedLines bool
	err          error
}

func readFileViewBounded(path string, maxLines int, maxLineBytes int, maxTotalBytes int) fileViewReadResult {
	file, err := os.Open(path)
	if err != nil {
		return fileViewReadResult{err: err}
	}
	defer file.Close()

	var lines []string
	var totalSourceBytes int
	truncated := false
	omittedLines := false

	// Enforce hard source-byte read limit to avoid reading unbounded data from disk.
	// We allow up to maxTotalBytes + 1 so we can detect truncation without reading to EOF.
	limitReader := io.LimitReader(file, int64(maxTotalBytes)+1)
	reader := bufio.NewReader(limitReader)

	for len(lines) < maxLines && totalSourceBytes < maxTotalBytes {
		var lineBuf []byte
		var lineTruncated bool

		for {
			chunk, isPrefix, err := reader.ReadLine()
			chunkLen := len(chunk)
			if chunkLen > 0 {
				remainTotal := maxTotalBytes - totalSourceBytes
				if remainTotal <= 0 {
					truncated = true
					omittedLines = true
					if len(lineBuf) > 0 {
						lines = append(lines, string(lineBuf))
					}
					goto finished
				}

				if chunkLen > remainTotal {
					chunk = chunk[:remainTotal]
					totalSourceBytes += remainTotal
					truncated = true
					omittedLines = true
					lineTruncated = true
				} else {
					totalSourceBytes += chunkLen
				}

				remainLine := maxLineBytes - len(lineBuf)
				if remainLine > 0 {
					if len(chunk) > remainLine {
						lineBuf = append(lineBuf, chunk[:remainLine]...)
						lineTruncated = true
					} else {
						lineBuf = append(lineBuf, chunk...)
					}
				} else {
					lineTruncated = true
				}
			}

			if err != nil {
				if len(lineBuf) > 0 {
					lines = append(lines, string(lineBuf))
					if lineTruncated {
						truncated = true
					}
				}
				if !errors.Is(err, io.EOF) {
					if len(lines) == 0 {
						return fileViewReadResult{err: err}
					}
					truncated = true
					omittedLines = true
				}
				goto finished
			}

			if totalSourceBytes >= maxTotalBytes {
				if isPrefix {
					truncated = true
					omittedLines = true
				} else if lineTruncated {
					truncated = true
				}
				if len(lineBuf) > 0 {
					lines = append(lines, string(lineBuf))
				}
				goto finished
			}

			if !isPrefix {
				break
			}
		}

		if lineTruncated {
			truncated = true
		}
		lines = append(lines, string(lineBuf))
		if totalSourceBytes >= maxTotalBytes {
			break
		}
	}

finished:
	if !omittedLines {
		if reader.Buffered() > 0 {
			truncated = true
			omittedLines = true
		} else if _, err := reader.Peek(1); err == nil {
			truncated = true
			omittedLines = true
		} else {
			var probe [1]byte
			if n, _ := file.Read(probe[:]); n > 0 {
				truncated = true
				omittedLines = true
			}
		}
	}

	return fileViewReadResult{
		lines:        lines,
		truncated:    truncated,
		omittedLines: omittedLines,
	}
}

func changedLinesFingerprint(changed map[string]bool) string {
	if len(changed) == 0 {
		return ""
	}
	keys := make([]string, 0, len(changed))
	for k, v := range changed {
		if v {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return strings.Join(keys, "\x00")
}

func formatFileViewLines(lines []string, display []string, changed map[string]bool, truncated bool, omittedLines bool, width int, theme tuiTheme) string {
	gutterW := len(fmt.Sprintf("%d", len(lines)))
	textBudget := maxInt(8, width-gutterW-3) // gutter + space + marker column

	var b strings.Builder
	for i, line := range display {
		line = fitStyledLine(line, textBudget)
		if i > 0 {
			b.WriteString("\n")
		}
		marker := " "
		if changed != nil && len(lines) > i && changed[strings.TrimSpace(lines[i])] {
			marker = theme.accent.Render("▎")
		}
		b.WriteString(theme.faintest.Render(fmt.Sprintf("%*d ", gutterW, i+1)))
		b.WriteString(marker)
		b.WriteString(line)
	}
	if truncated {
		// No exact remaining-line count: computing one would require reading the
		// rest of the file, defeating the bounded read above.
		b.WriteString("\n")
		if omittedLines {
			b.WriteString(theme.faint.Render(fmt.Sprintf("… more lines (file truncated at %d for display)", len(lines))))
		} else {
			b.WriteString(theme.faint.Render("… (line content truncated at display limit)"))
		}
	}
	return b.String()
}

// peekRenderOnly looks up an already-formatted variant in memory. It performs
// strictly 0 I/O and 0 string formatting/allocations, guaranteeing O(1) instantaneous
// access on the View() drawing path.
func (c *fileViewRenderCache) peekRenderOnly(targetPath string, width int, changedFingerprint string) (string, bool) {
	c.mu.Lock()
	elem, ok := c.items[targetPath]
	if !ok {
		c.mu.Unlock()
		return "", false
	}
	entry := elem.Value.(*fileViewCachedEntry)
	c.lru.MoveToFront(elem)
	c.statsData.CacheHits++
	c.mu.Unlock()

	renderKey := fmt.Sprintf("%d:%s", width, changedFingerprint)
	return entry.getRender(renderKey)
}

// loadAndRender performs the bounded read, Chroma highlighting, and formatting
// on a cache miss (or re-formats for a new width variant on a cache hit). It is
// intended to be executed from a tea.Cmd / background worker, off the View path.
func (c *fileViewRenderCache) loadAndRender(targetPath string, displayPath string, width int, changed map[string]bool, changedFingerprint string, reqGen int, theme tuiTheme) (string, error) {
	stat, err := os.Stat(targetPath)
	if err != nil {
		c.mu.Lock()
		if elem, ok := c.items[targetPath]; ok {
			c.lru.Remove(elem)
			delete(c.items, targetPath)
		}
		c.mu.Unlock()
		rendered := theme.faint.Render("Could not read file: " + err.Error())
		return rendered, err
	}

	modTime := stat.ModTime()
	size := stat.Size()
	renderKey := fmt.Sprintf("%d:%s", width, changedFingerprint)

	c.mu.Lock()
	if c.gen != reqGen {
		c.mu.Unlock()
		return "", errors.New("request superseded by cache invalidation")
	}

	if elem, ok := c.items[targetPath]; ok {
		entry := elem.Value.(*fileViewCachedEntry)
		if entry.modTime.Equal(modTime) && entry.size == size && entry.displayPath == displayPath {
			c.statsData.CacheHits++
			c.lru.MoveToFront(elem)
			c.mu.Unlock()

			if rendered, ok := entry.getRender(renderKey); ok {
				return rendered, nil
			}

			// Re-format for the new width or changed markers using cached display and lines
			rendered := formatFileViewLines(entry.lines, entry.display, changed, entry.truncated, entry.omittedLines, width, theme)
			entry.putRender(renderKey, rendered)
			return rendered, nil
		}
	}

	c.statsData.CacheMisses++
	c.statsData.DiskReads++
	c.mu.Unlock()

	readRes := readFileViewBounded(targetPath, fileViewMaxLines, fileViewMaxLineBytes, fileViewMaxBytes)
	if readRes.err != nil && len(readRes.lines) == 0 {
		c.mu.Lock()
		if elem, ok := c.items[targetPath]; ok {
			c.lru.Remove(elem)
			delete(c.items, targetPath)
		}
		c.mu.Unlock()
		rendered := theme.faint.Render("Could not read file: " + readRes.err.Error())
		return rendered, readRes.err
	}

	c.mu.Lock()
	c.statsData.HighlightCalls++
	c.mu.Unlock()

	display, ok := highlightCodeForPathWithTheme(readRes.lines, displayPath, 1<<20, nil, theme)
	if !ok || len(display) != len(readRes.lines) {
		display = readRes.lines
	}

	rendered := formatFileViewLines(readRes.lines, display, changed, readRes.truncated, readRes.omittedLines, width, theme)

	entry := &fileViewCachedEntry{
		targetPath:   targetPath,
		displayPath:  displayPath,
		modTime:      modTime,
		size:         size,
		lines:        readRes.lines,
		display:      display,
		truncated:    readRes.truncated,
		omittedLines: readRes.omittedLines,
		renderKeys:   []string{renderKey},
		renders:      map[string]string{renderKey: rendered},
	}

	c.mu.Lock()
	if c.gen != reqGen {
		c.mu.Unlock()
		return "", errors.New("request superseded by cache invalidation")
	}

	if elem, ok := c.items[targetPath]; ok {
		existing := elem.Value.(*fileViewCachedEntry)
		if existing.modTime.After(modTime) {
			c.mu.Unlock()
			return rendered, nil
		}
		c.lru.Remove(elem)
		delete(c.items, targetPath)
	}
	elem := c.lru.PushFront(entry)
	c.items[targetPath] = elem

	for len(c.items) > c.maxEntries {
		back := c.lru.Back()
		if back == nil {
			break
		}
		backEntry := back.Value.(*fileViewCachedEntry)
		delete(c.items, backEntry.targetPath)
		c.lru.Remove(back)
	}
	c.mu.Unlock()

	return rendered, nil
}

func (c *fileViewRenderCache) getOrRender(targetPath string, displayPath string, width int, changed map[string]bool) string {
	fingerprint := changedLinesFingerprint(changed)
	rendered, _ := c.loadAndRender(targetPath, displayPath, width, changed, fingerprint, c.generation(), zeroTheme)
	return rendered
}

// fileViewLoadedMsg delivers the result of an asynchronous file read & render.
type fileViewLoadedMsg struct {
	lifetimeToken [16]byte
	seq           uint64
	generation    int
	targetPath    string
	displayPath   string
	width         int
	fingerprint   string
	rendered      string
	err           error
}

func loadFileViewCmd(targetPath string, displayPath string, width int, changed map[string]bool, fingerprint string, token [16]byte, seq uint64, gen int, theme tuiTheme) tea.Cmd {
	return func() tea.Msg {
		rendered, err := defaultFileViewCache.loadAndRender(targetPath, displayPath, width, changed, fingerprint, gen, theme)
		return fileViewLoadedMsg{
			lifetimeToken: token,
			seq:           seq,
			generation:    gen,
			targetPath:    targetPath,
			displayPath:   displayPath,
			width:         width,
			fingerprint:   fingerprint,
			rendered:      rendered,
			err:           err,
		}
	}
}

// fileViewState manages the drill-in view for a touched file. When active, the
// transcript body swaps to the file's diff/content instead of the chat rows.
type fileViewState struct {
	active             bool
	path               string // workspace-relative, as carried by changedFiles
	mode               int    // fileViewDiff | fileViewFull
	parentScrollOffset int

	// View session lifetime identity (UUIDv7 RFC 9562 0-alloc)
	lifetimeToken [16]byte

	// Monotonically advancing desired snapshot sequence & requested parameters
	desiredSeq         uint64
	desiredWidth       int
	desiredFingerprint string
	desiredGen         int

	// Authoritative completed snapshot (only valid when loadedSeq == desiredSeq)
	renderedContent   string
	loadedPath        string
	loadedWidth       int
	loadedGen         int
	loadedFingerprint string
	loadedToken       [16]byte
	loadedSeq         uint64
	loading           bool
	hasError          bool
}

func (m model) startFileViewLoadCmd(width int) (model, tea.Cmd) {
	if !m.fileView.active || m.fileView.mode != fileViewFull || m.fileView.path == "" {
		return m, nil
	}
	target := m.fileView.path
	if !filepath.IsAbs(target) {
		target = filepath.Join(m.cwd, target)
	}
	m.fileView.desiredSeq++
	m.fileView.desiredWidth = width
	m.fileView.loading = true
	seq := m.fileView.desiredSeq
	token := m.fileView.lifetimeToken
	gen := defaultFileViewCache.generation()
	changed := m.fileViewChangedLines()
	fingerprint := changedLinesFingerprint(changed)
	m.fileView.desiredFingerprint = fingerprint
	m.fileView.desiredGen = gen
	theme := zeroTheme
	return m, loadFileViewCmd(target, m.fileView.path, width, changed, fingerprint, token, seq, gen, theme)
}

// openFileView activates the drill-in for path in diff mode. Opening from an
// already-open view (clicking another FILES row) keeps the original saved chat
// scroll position rather than saving the file view's own offset as "parent".
// Re-opening the file that is ALREADY being viewed is a no-op: a stray
// re-click must not bounce the user from full mode back to diff or reset
// their scroll position.
func (m model) openFileView(path string) (model, tea.Cmd) {
	if m.fileView.active && m.fileView.path == path {
		return m, nil
	}
	if !m.fileView.active {
		m.fileView.parentScrollOffset = m.chatScrollOffset
	}
	m.fileView.active = true
	m.fileView.path = path
	m.fileView.mode = fileViewDiff
	m.fileView.lifetimeToken = nextFileViewLifetimeToken()
	m.fileView.renderedContent = ""
	m.fileView.loadedToken = [16]byte{}
	m.fileView.loadedSeq = 0
	m.fileView.hasError = false
	// A file only the git sweep knows about (bash/subagent mutation) has no edit
	// cards to stack — open straight on the full file instead of a placeholder.
	if len(m.fileViewResultRows()) == 0 {
		m.fileView.mode = fileViewFull
	}
	m.chatScrollOffset = 0
	m = m.clearHover() // bodyY numbering differs between the file body and the chat
	if m.fileView.mode == fileViewFull {
		return m.startFileViewLoadCmd(m.chatColumnWidth())
	}
	return m, nil
}

// exitFileView deactivates the view and restores the chat scroll position.
func (m model) exitFileView() model {
	if !m.fileView.active {
		return m
	}
	m.chatScrollOffset = m.fileView.parentScrollOffset
	m.fileView = fileViewState{}
	m = m.clearHover()
	return m
}

// setFileViewMode switches diff/full, resetting the scroll to the bottom-anchored
// start since the two bodies have unrelated heights.
func (m model) setFileViewMode(mode int) (model, tea.Cmd) {
	if !m.fileView.active || m.fileView.mode == mode {
		return m, nil
	}
	m.fileView.mode = mode
	m.chatScrollOffset = 0
	if mode == fileViewFull {
		return m.startFileViewLoadCmd(m.chatColumnWidth())
	}
	return m, nil
}

func (m model) handleFileViewLoaded(msg fileViewLoadedMsg) (model, tea.Cmd) {
	if !m.fileView.active || m.fileView.mode != fileViewFull {
		return m, nil
	}
	// 1. Session lifetime identity match
	if m.fileView.lifetimeToken != msg.lifetimeToken || m.fileView.path != msg.displayPath {
		return m, nil
	}
	// 2. Exact desired snapshot match: reject superseded / out-of-order completions
	if msg.seq != m.fileView.desiredSeq ||
		msg.width != m.fileView.desiredWidth ||
		msg.fingerprint != m.fileView.desiredFingerprint ||
		msg.generation != defaultFileViewCache.generation() {
		if msg.generation != defaultFileViewCache.generation() {
			// Cache was invalidated (e.g. theme switch) while this request was in flight.
			return m.startFileViewLoadCmd(m.chatColumnWidth())
		}
		// Out-of-order or obsolete completion: drop without modifying state
		return m, nil
	}
	m.fileView.loading = false
	m.fileView.renderedContent = msg.rendered
	m.fileView.loadedPath = msg.displayPath
	m.fileView.loadedWidth = msg.width
	m.fileView.loadedGen = msg.generation
	m.fileView.loadedFingerprint = msg.fingerprint
	m.fileView.loadedToken = msg.lifetimeToken
	m.fileView.loadedSeq = msg.seq
	m.fileView.hasError = (msg.err != nil)
	return m, nil
}

// fileViewNavBar renders the single-line header shown in place of the pinned
// title bar while the view is active: the path plus the key hints. One line
// exactly, so every scrollableTranscriptFrame computed against it agrees with
// the title bar's geometry.
func (m model) fileViewNavBar(width int) string {
	mode := "diff"
	other := "f full"
	if m.fileView.mode == fileViewFull {
		mode = "full"
		other = "d diff"
	}
	left := zeroTheme.accent.Render("← "+truncatePathLeft(m.fileView.path, maxInt(8, width/2))) +
		zeroTheme.faint.Render("  ·  "+mode)
	right := zeroTheme.faint.Render(other + " · esc back")
	return fitStyledLine(joinHeaderLine(left, right, width), width)
}

// fileViewBodyItems builds the body items the transcript machinery renders
// while the view is active — one pre-rendered block, so scrolling and height
// accounting flow through the exact same path as chat rows.
func (m model) fileViewBodyItems(width int) []transcriptBodyItem {
	var block string
	if m.fileView.mode == fileViewFull {
		block = m.renderFileViewFull(width)
	} else {
		block = m.renderFileViewDiff(width)
	}
	return []transcriptBodyItem{transcriptBlockBodyItem(transcriptBodyItemRow, -1, block)}
}

// fileViewResultRows returns the transcript's tool-result rows that touched the
// viewed file, in chronological order.
func (m model) fileViewResultRows() []transcriptRow {
	var rows []transcriptRow
	for _, row := range m.transcript {
		if row.kind != rowToolResult {
			continue
		}
		for _, p := range row.changedFiles {
			if p == m.fileView.path {
				rows = append(rows, row)
				break
			}
		}
	}
	return rows
}

// renderFileViewDiff renders the session's edits to the file as its full-depth
// tool cards (bodyCap 0, the detailed-view depth), stacked chronologically —
// the same cards the chat shows, so the diffs read identically in both places.
func (m model) renderFileViewDiff(width int) string {
	rows := m.fileViewResultRows()
	if len(rows) == 0 {
		return zeroTheme.faint.Render("No recorded edits for this file in this session.")
	}
	rc := buildRowContext(m.transcript)
	opts := cardRenderOptions{bodyCap: 0, cwd: m.cwd}
	var b strings.Builder
	for i, row := range rows {
		if i > 0 {
			b.WriteString("\n\n")
		}
		if len(rows) > 1 {
			b.WriteString(zeroTheme.faint.Render(fmt.Sprintf("edit %d of %d", i+1, len(rows))))
			b.WriteString("\n")
		}
		b.WriteString(m.renderRowModeUncached(row, width, rc, opts))
	}
	return b.String()
}

// renderFileViewFull renders the file as it currently stands on disk, syntax
// highlighted, with a line-number gutter and an accent ▎ marker on the lines
// this session's diffs added (matched by exact text — an approximation that
// tolerates later drift; a stale marker just doesn't highlight).
// It is strictly non-blocking and performs O(1) lookup without invoking formatters.
func (m model) renderFileViewFull(width int) string {
	if m.fileView.hasError {
		return m.fileView.renderedContent
	}
	target := m.fileView.path
	if !filepath.IsAbs(target) {
		target = filepath.Join(m.cwd, target)
	}
	if cached, ok := defaultFileViewCache.peekRenderOnly(target, width, m.fileView.desiredFingerprint); ok {
		return cached
	}
	if m.fileView.renderedContent != "" &&
		m.fileView.loadedPath == m.fileView.path &&
		m.fileView.loadedSeq == m.fileView.desiredSeq &&
		m.fileView.loadedGen == defaultFileViewCache.generation() &&
		m.fileView.loadedToken == m.fileView.lifetimeToken {
		return m.fileView.renderedContent
	}
	return zeroTheme.faint.Render(fileViewLoadingPlaceholder)
}

// fileViewChangedLines collects the trimmed text of every line the session's
// diffs ADDED to the viewed file, for the full-mode gutter markers. Very short
// lines ("}", "return", ")") are skipped: text-matching them would mark every
// unrelated occurrence across the file, which misleads far more than a missing
// marker on a brace line ever could.
func (m model) fileViewChangedLines() map[string]bool {
	changed := map[string]bool{}
	for _, row := range m.fileViewResultRows() {
		for _, line := range strings.Split(row.detail, "\n") {
			if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
				continue
			}
			if text := strings.TrimSpace(strings.TrimPrefix(line, "+")); len(text) >= 4 {
				changed[text] = true
			}
		}
	}
	return changed
}
