package tools

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// HTML-to-markdown conversion for web_fetch. Raw HTML is mostly boilerplate —
// on a typical page well under a tenth of the bytes are readable content — so
// returning it raw wastes nearly the whole response budget on markup. The
// converter is deliberately dependency-free (regex passes + stdlib entity
// unescaping, no HTML parser): the output feeds a language model, not a
// browser, so best-effort structure (headings, links, list markers) is enough,
// and the format=raw escape hatch covers pages it mangles.

var (
	webFetchDropBlockRe = regexp.MustCompile(`(?is)<(script|style|head|noscript|svg|template|iframe)\b[^>]*>.*?</\s*(?:script|style|head|noscript|svg|template|iframe)\s*>`)
	webFetchCommentRe   = regexp.MustCompile(`(?s)<!--.*?-->`)
	webFetchAnchorRe    = regexp.MustCompile(`(?is)<a\b[^>]*?href\s*=\s*["']?([^"'\s>]+)["']?[^>]*>(.*?)</\s*a\s*>`)
	webFetchHeadingRe   = regexp.MustCompile(`(?is)<h([1-6])[^>]*>(.*?)</\s*h[1-6]\s*>`)
	webFetchListItemRe  = regexp.MustCompile(`(?i)<li\b[^>]*>`)
	webFetchBreakRe     = regexp.MustCompile(`(?i)<(?:br|hr)\s*/?>`)
	webFetchBlockTagRe  = regexp.MustCompile(`(?i)</?(?:p|div|section|article|main|header|footer|nav|aside|table|tr|ul|ol|blockquote|pre|figure|form)\b[^>]*>`)
	webFetchAnyTagRe    = regexp.MustCompile(`(?s)<[^>]*>`)
	webFetchSpaceRunRe  = regexp.MustCompile(`[ \t]+`)
	webFetchBlankRunRe  = regexp.MustCompile(`\n{3,}`)
)

// htmlToMarkdown converts an HTML document to compact markdown-flavored text:
// headings become #-prefixed lines, anchors become [text](href), list items
// become "- " bullets, block boundaries become blank lines, all other markup
// is stripped, and entities are unescaped.
func htmlToMarkdown(body string) string {
	text := webFetchCommentRe.ReplaceAllString(body, " ")
	text = webFetchDropBlockRe.ReplaceAllString(text, " ")
	text = webFetchHeadingRe.ReplaceAllStringFunc(text, func(match string) string {
		groups := webFetchHeadingRe.FindStringSubmatch(match)
		level := int(groups[1][0] - '0')
		if level < 1 || level > 6 {
			level = 1
		}
		title := strings.TrimSpace(webFetchAnyTagRe.ReplaceAllString(groups[2], " "))
		return "\n\n" + strings.Repeat("#", level) + " " + title + "\n\n"
	})
	text = webFetchAnchorRe.ReplaceAllStringFunc(text, func(match string) string {
		groups := webFetchAnchorRe.FindStringSubmatch(match)
		href := groups[1]
		label := strings.TrimSpace(webFetchAnyTagRe.ReplaceAllString(groups[2], " "))
		if label == "" {
			return " "
		}
		if strings.HasPrefix(href, "#") || strings.HasPrefix(strings.ToLower(href), "javascript:") {
			return label
		}
		return fmt.Sprintf("[%s](%s)", label, href)
	})
	text = webFetchListItemRe.ReplaceAllString(text, "\n- ")
	text = webFetchBreakRe.ReplaceAllString(text, "\n")
	text = webFetchBlockTagRe.ReplaceAllString(text, "\n\n")
	text = webFetchAnyTagRe.ReplaceAllString(text, " ")
	text = html.UnescapeString(text)

	// Whitespace normalization: collapse space runs, trim line edges, cap blank
	// runs at one empty line.
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(webFetchSpaceRunRe.ReplaceAllString(line, " "))
	}
	text = strings.Join(lines, "\n")
	text = webFetchBlankRunRe.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

// looksLikeHTML reports whether a response should be treated as an HTML
// document, by Content-Type first and a body sniff as fallback (servers
// mislabel HTML as text/plain often enough to matter).
func looksLikeHTML(contentType, body string) bool {
	lowered := strings.ToLower(contentType)
	if strings.Contains(lowered, "text/html") || strings.Contains(lowered, "application/xhtml") {
		return true
	}
	if lowered != "" && !strings.Contains(lowered, "text/plain") {
		return false
	}
	head := strings.ToLower(strings.TrimSpace(body))
	if len(head) > 512 {
		head = head[:512]
	}
	return strings.Contains(head, "<!doctype html") || strings.Contains(head, "<html")
}
