package cli

import (
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	previewFilePreview = iota
	previewFileOpen
	previewFileCancel
)

var previewFileChoices = [...]string{
	previewFilePreview: "Preview",
	previewFileOpen:    "Open with default app",
	previewFileCancel:  "Cancel",
}

type previewFileOpenedMsg struct {
	request uint64
	result  libraryOpenedMsg
}

func (m sendFileModel) updatePreviewChoice(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "left", "up", "shift+tab":
		m.choice = (m.choice + len(previewFileChoices) - 1) % len(previewFileChoices)
	case "right", "down", "tab":
		m.choice = (m.choice + 1) % len(previewFileChoices)
	case "esc", "ctrl+c", "n":
		m.confirm = false
	case "enter":
		m.confirm = false
		switch m.choice {
		case previewFilePreview:
			m.accepted = true
			return m, tea.Quit
		case previewFileOpen:
			m.openRequest++
			request, path := m.openRequest, m.path
			return m, func() tea.Msg {
				return previewFileOpenedMsg{request, libraryOpenedMsg{openLibraryFile(path)}}
			}
		}
	}
	return m, nil
}

func (m sendFileModel) previewChoiceView(background string) string {
	if m.width < 32 || m.height < 14 {
		lines := []string{m.target.confirmTitle}
		if m.height >= 6 {
			lines = append(lines, bookCell(filepath.Base(m.path)))
		}
		for i, label := range previewFileChoices {
			if i == previewFileOpen && m.width < len(label)+2 {
				label = "Open default app"
			}
			button := renderActionButton(label, i == m.choice, false)
			lines = append(lines, ansi.Truncate(button, max(1, m.width), ""))
		}
		lines = append(lines, "↑/↓ · Enter · Esc")
		return fullScreenView(m.width, m.height, strings.Join(lines, "\n")).Content
	}
	inner := min(52, m.width-6)
	base := lipgloss.NewStyle().Foreground(currentTheme.Title).Background(currentTheme.Surface)
	lines := []string{
		base.Bold(true).Render(m.target.confirmTitle),
		"",
		ansi.Truncate(bookCell(filepath.Base(m.path)), inner, "…"),
		"",
	}
	lines = append(lines, actionButtonRowsOn(previewFileChoices[:], m.choice+1, inner, base)...)
	lines = append(lines, "", "←/→ select · Enter · Esc")
	return confirmationPanel(background, m.width, m.height, inner, base, lines)
}
