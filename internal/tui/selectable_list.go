package tui

type selectableListItem struct {
	Label       string
	Description string
}

type selectableListOptions struct {
	Items      []selectableListItem
	Selected   int
	Width      int
	MaxVisible int
}

const selectableListAnchorRow = 3

func selectableListStart(total, maxVisible, selected int) int {
	if total <= maxVisible {
		return 0
	}
	start := selected - selectableListAnchorRow
	return clampInt(start, 0, total-maxVisible)
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
