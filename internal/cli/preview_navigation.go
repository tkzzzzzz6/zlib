package cli

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

func (m *previewPageModel) scrollHelp(k string) {
	m.helpOffset = listCursor(k, m.helpOffset, max(1, m.helpRowCount()-m.visibleRows()+1), m.visibleRows())
}

func (m previewPageModel) openJump() (tea.Model, tea.Cmd) {
	m.jumping, m.jumpError = true, ""
	m.jumpInput = textinput.New()
	m.jumpInput.Placeholder = fmt.Sprintf("1–%d", m.page.Count)
	m.jumpInput.SetWidth(max(1, m.width-3))
	m.jumpInput.KeyMap.Paste.SetKeys("ctrl+v", "ctrl+shift+v", "shift+insert")
	cmd := m.jumpInput.Focus()
	return m, cmd
}

func (m previewPageModel) updateJump(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "esc", "ctrl+c":
			m.jumping = false
			m.jumpInput.Blur()
			return m, nil
		case "enter":
			n, err := strconv.ParseUint(strings.TrimSpace(m.jumpInput.Value()), 10, strconv.IntSize)
			if err != nil || n == 0 || n > uint64(m.page.Count) {
				m.jumpError = fmt.Sprintf("Enter a number from 1 to %d.", m.page.Count)
				return m, nil
			}
			m.jumping, m.contents = false, false
			m.jumpInput.Blur()
			if index := int(n) - 1; index != m.index {
				return m.load(index)
			}
			return m, nil
		}
	}
	previous := m.jumpInput.Value()
	var cmd tea.Cmd
	m.jumpInput, cmd = m.jumpInput.Update(msg)
	if previous != m.jumpInput.Value() {
		m.jumpError = ""
	}
	return m, cmd
}

func (m previewPageModel) jumpView() tea.View {
	unit := "page"
	if len(m.page.Chapters) > 0 {
		unit = "chapter"
	}
	return m.previewView("Go to "+unit, fmt.Sprintf("Current %s %d of %d", unit, m.index+1, m.page.Count), m.jumpInput.View(), m.jumpError,
		uiKey("enter", "go"), uiKey("esc", "cancel"))
}
