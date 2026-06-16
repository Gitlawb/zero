package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

func testKey(code rune) tea.KeyPressMsg {
	text := ""
	if code == tea.KeySpace {
		text = " "
	}
	return tea.KeyPressMsg(tea.Key{Code: code, Text: text})
}

func testKeyText(text string) tea.KeyPressMsg {
	runes := []rune(text)
	code := tea.KeyExtended
	if len(runes) == 1 {
		code = runes[0]
	}
	return tea.KeyPressMsg(tea.Key{Code: code, Text: text})
}

func testKeyAlt(code rune) tea.KeyPressMsg {
	return testKeyPressMod(code, tea.ModAlt)
}

func testKeyAltText(text string) tea.KeyPressMsg {
	msg := testKeyText(text)
	key := msg.Key()
	key.Mod = tea.ModAlt
	return tea.KeyPressMsg(key)
}

func testKeyCtrl(code rune) tea.KeyPressMsg {
	return testKeyPressMod(code, tea.ModCtrl)
}

func testKeyShift(code rune) tea.KeyPressMsg {
	return testKeyPressMod(code, tea.ModShift)
}

func testKeyPressMod(code rune, mod tea.KeyMod) tea.KeyPressMsg {
	msg := testKey(code)
	key := msg.Key()
	key.Mod = mod
	return tea.KeyPressMsg(key)
}

func testPaste(content string) tea.PasteMsg {
	return tea.PasteMsg{Content: content}
}

func testMouseClick(button tea.MouseButton, x int, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg(tea.Mouse{Button: button, X: x, Y: y})
}

func testMouseWheel(button tea.MouseButton, x int, y int) tea.MouseWheelMsg {
	return tea.MouseWheelMsg(tea.Mouse{Button: button, X: x, Y: y})
}

func testMouseMotion(button tea.MouseButton, x int, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg(tea.Mouse{Button: button, X: x, Y: y})
}

func testMouseRelease(button tea.MouseButton, x int, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg(tea.Mouse{Button: button, X: x, Y: y})
}

func viewString(view tea.View) string {
	return view.Content
}

func renderContent(rendered any) string {
	switch v := rendered.(type) {
	case string:
		return v
	case tea.View:
		return v.Content
	default:
		return fmt.Sprint(v)
	}
}
