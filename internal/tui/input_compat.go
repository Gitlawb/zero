package tui

import tea "charm.land/bubbletea/v2"

func keyCode(msg tea.KeyMsg) rune {
	return msg.Key().Code
}

func keyText(msg tea.KeyMsg) string {
	return msg.Key().Text
}

func keyRunes(msg tea.KeyMsg) []rune {
	return []rune(keyText(msg))
}

func keyHasMod(msg tea.KeyMsg, mod tea.KeyMod) bool {
	return msg.Key().Mod.Contains(mod)
}

func keyAlt(msg tea.KeyMsg) bool {
	return keyHasMod(msg, tea.ModAlt)
}

func keyShift(msg tea.KeyMsg) bool {
	return keyHasMod(msg, tea.ModShift)
}

func keyIs(msg tea.KeyMsg, code rune) bool {
	return keyCode(msg) == code
}

func keyCtrl(msg tea.KeyMsg, code rune) bool {
	return keyCode(msg) == code && keyHasMod(msg, tea.ModCtrl)
}

// keySuper matches the Command key on macOS, which the rest of the TUI models as
// ModSuper (see keybindings.go). Kept separate from keyCtrl because a binding
// that means Ctrl on Linux and Windows usually means Command on macOS, and the
// two arrive as different modifiers.
func keySuper(msg tea.KeyMsg, code rune) bool {
	return keyCode(msg) == code && keyHasMod(msg, tea.ModSuper)
}

func keyCtrlArrow(msg tea.KeyMsg, code rune) bool {
	return keyIs(msg, code) && keyHasMod(msg, tea.ModCtrl)
}

func keyPrintable(msg tea.KeyMsg) bool {
	return keyText(msg) != "" && !keyAlt(msg) && !keyHasMod(msg, tea.ModCtrl)
}

func keyBackspace(msg tea.KeyMsg) bool {
	return keyIs(msg, tea.KeyBackspace) || keyCtrl(msg, 'h')
}

func mouseEvent(msg tea.MouseMsg) tea.Mouse {
	return msg.Mouse()
}

func mouseX(msg tea.MouseMsg) int {
	return mouseEvent(msg).X
}

func mouseY(msg tea.MouseMsg) int {
	return mouseEvent(msg).Y
}

func mouseLeftPress(msg tea.MouseMsg) bool {
	event := mouseEvent(msg)
	return event.Button == tea.MouseLeft && isMouseClick(msg)
}

func mouseRightPress(msg tea.MouseMsg) bool {
	event := mouseEvent(msg)
	return event.Button == tea.MouseRight && isMouseClick(msg)
}

func mouseMotion(msg tea.MouseMsg) bool {
	_, ok := msg.(tea.MouseMotionMsg)
	return ok
}

func mouseRelease(msg tea.MouseMsg) bool {
	_, ok := msg.(tea.MouseReleaseMsg)
	return ok
}

func mouseWheelUp(msg tea.MouseMsg) bool {
	event := mouseEvent(msg)
	return event.Button == tea.MouseWheelUp
}

func mouseWheelDown(msg tea.MouseMsg) bool {
	event := mouseEvent(msg)
	return event.Button == tea.MouseWheelDown
}

func isMouseClick(msg tea.MouseMsg) bool {
	_, ok := msg.(tea.MouseClickMsg)
	return ok
}
