# TUI Themes & Layout Presets

This document details the architectural plan to add custom UI theme presets to Zero. Swappable themes allow the developer to customize Zero's appearance, density, and panel layouts according to their preferences.

---

## 🎨 Layout & Design Presets

### 1. Claude Theme (`claude`)
*   **Aesthetic**: Warm, card-based, clean, low-density.
*   **Colors**: Sand/cream borders, soft amber highlights, charcoal body text.
*   **Layout**: Centered single-column message feed with a right-aligned collapsable side-panel drawer.

### 2. Codex Theme (`codex`)
*   **Aesthetic**: Matrix-green/cyan cyberpunk theme, high-density monospace layout.
*   **Colors**: Vibrant neon-greens and neon-cyans on pitch-black background.
*   **Layout**: Split-pane view (Left: Chat feed / Right: Real-time daemon & command execution timeline) with a bottom grid of keyboard shortcuts.

---

## 🏗️ Interface Architecture

Theme customization is governed by the `Theme` interface in `internal/tui/theme.go`:

```go
package tui

import "github.com/charmbracelet/lipgloss"

type Theme interface {
    Name() string
    ChatStyle() lipgloss.Style
    MessageStyle(sender string) lipgloss.Style
    SidebarStyle() lipgloss.Style
    Border() lipgloss.Border
    BorderStyle() lipgloss.Style
}
```

---

## 🚀 Implementation Roadmap

1.  **Introduce user configuration settings** inside `config.json` via a new `tui.theme` key.
2.  **Expose `Theme` settings** dynamically inside `internal/tui/model.go` using BubbleTea.
3.  **Refactor rendering methods** in the main model (such as `View()`) to compose panels dynamically via `lipgloss.JoinHorizontal` or `lipgloss.JoinVertical` depending on the active theme layout.
