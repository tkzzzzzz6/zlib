package cli

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/heartleo/zlib/internal/preview"
)

// first:end follows the document's reading order even for backwards drags.
type previewSelection struct {
	first, end int
	anchor     int
	dragging   bool
	feedback   string
}

type previewCopiedMsg struct {
	generation, first, end int
	err                    error
}

func (m previewPageModel) hasSelection() bool {
	return m.selection.first < m.selection.end && m.selection.end <= len(m.page.Words)
}

func (m *previewPageModel) selectWord(event previewImagePointerMsg) {
	if m.help || m.jumping || !m.showingImage() {
		return
	}
	s := &m.selection
	hit := event.word >= 0 && event.word < len(m.page.Words)
	switch mouse := event.mouse.(type) {
	case tea.MouseClickMsg:
		if mouse.Button != tea.MouseLeft || mouse.Mod != 0 {
			return
		}
		*s = previewSelection{}
		if hit {
			s.first, s.end, s.anchor, s.dragging = event.word, event.word+1, event.word, true
		}
		return
	case tea.MouseMotionMsg:
		if !s.dragging || mouse.Button != tea.MouseLeft {
			return
		}
	case tea.MouseReleaseMsg:
		if !s.dragging || (mouse.Button != tea.MouseLeft && mouse.Button != tea.MouseNone) {
			return
		}
		s.dragging = false
	default:
		return
	}
	if hit {
		first, end := min(s.anchor, event.word), max(s.anchor, event.word)+1
		if s.first != first || s.end != end {
			s.first, s.end, s.feedback = first, end, ""
		}
	}
}

func (m previewPageModel) copySelection() tea.Cmd {
	if !m.hasSelection() {
		return nil
	}
	s := m.selection
	words := m.page.Words[s.first:s.end]
	return func() tea.Msg {
		return previewCopiedMsg{m.generation, s.first, s.end, m.writeClipboard(preview.TextFromWords(words))}
	}
}

func (m *previewPageModel) copied(msg previewCopiedMsg) {
	s := &m.selection
	if !m.hasSelection() || msg.generation != m.generation || msg.first != s.first || msg.end != s.end {
		return
	}
	if msg.err != nil {
		s.feedback = "Copy failed: " + bookCell(safeText(msg.err.Error()))
	} else {
		s.feedback = "Copied selection to clipboard."
	}
}

func (m previewPageModel) imageStatus() string {
	switch {
	case m.selection.feedback != "":
		return m.selection.feedback
	case m.hasSelection():
		count := m.selection.end - m.selection.first
		if count == 1 {
			return "1 word selected · y copy"
		}
		return fmt.Sprintf("%d words selected · y copy", count)
	case m.notice != "":
		return m.notice
	case m.page.WordsError != "":
		return "Text selection unavailable: " + bookCell(safeText(m.page.WordsError))
	case len(m.page.Words) == 0:
		return "No selectable text layer on this page."
	case m.zoomPercent > 100:
		return "↑/↓ pan vertically · shift+←/→ pan horizontally"
	default:
		return "Drag over text to select · y copy"
	}
}
