package cli

import (
	"context"
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"time"

	keys "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/heartleo/zlib/internal/preview"
)

type previewGraphicsMsg bool

type previewLoadedMsg struct {
	generation     int
	page           preview.Page
	err            error
	historyWarning string
}

type previewPageModel struct {
	path                                     string
	width, height, index, offset, generation int
	page                                     preview.Page
	document                                 *preview.Document
	lines                                    []string
	busy, graphics, textOnly, contents       bool
	help, jumping                            bool
	helpOffset                               int
	jumpInput                                textinput.Model
	jumpError                                string
	upgradeImage                             bool
	chapter, chapterTop                      int
	zoomPercent, panX, panY                  int
	notice                                   string
	ctx                                      context.Context
	cancel                                   context.CancelFunc
}

func newPreviewPage(path string) previewPageModel {
	ctx, cancel := context.WithCancel(context.Background())
	return previewPageModel{path: path, document: preview.NewDocument(path), width: 80, height: 24, zoomPercent: 100, busy: true, graphics: coverGraphicsAvailable.Load(), ctx: ctx, cancel: cancel}
}

func (m previewPageModel) Init() tea.Cmd {
	return func() tea.Msg {
		page, err := m.document.Load(m.ctx, m.index, m.graphics && !m.textOnly)
		result := previewLoadedMsg{generation: m.generation, page: page, err: err, historyWarning: m.notice}
		if err == nil && m.page.Count == 0 && m.ctx.Err() == nil {
			if err := recordBookOpened(m.path); err != nil {
				result.historyWarning = "Could not update reading history: " + bookCell(safeText(err.Error()))
			}
		}
		return result
	}
}

func (m previewPageModel) load(index int) (tea.Model, tea.Cmd) {
	// A single turn starts immediately. When more turns arrive before its
	// result, wait for the wheel to settle before starting another renderer.
	coalesce := m.busy && m.page.Count > 0 && index != m.index && strings.EqualFold(filepath.Ext(m.path), ".pdf")
	m.cancel()
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.generation++
	m.index, m.offset, m.busy, m.contents, m.upgradeImage, m.notice = index, 0, true, false, false, ""
	load := m.Init()
	if !coalesce {
		return m, load
	}
	return m, func() tea.Msg {
		timer := time.NewTimer(35 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return load()
		case <-m.ctx.Done():
			return nil
		}
	}
}

func (m previewPageModel) visibleRows() int { return max(1, m.height-6) }

func (m previewPageModel) nextTextScreen() bool {
	return !m.showingImage() && !m.page.EmptyChapter() && m.offset+m.visibleRows() < len(m.lines)
}

func (m *previewPageModel) wrap() {
	width := max(1, m.width)
	text := safeText(ansi.Strip(m.page.Text))
	if m.page.EmptyChapter() {
		actions := []string{"c Chapters", "o Open in reader"}
		if m.page.Index+1 < m.page.Count {
			actions = append([]string{"→ Next chapter"}, actions...)
		} else if m.page.Index > 0 {
			actions = append([]string{"← Previous chapter"}, actions...)
		}
		text = ansi.Wordwrap("This chapter has no previewable text.\n\n"+strings.Join(actions, "    "), width, "")
	}
	m.lines = strings.Split(ansi.Hardwrap(text, width, true), "\n")
	m.offset = min(m.offset, max(0, len(m.lines)-m.visibleRows()))
}

func (m previewPageModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		m.wrap()
		m.chapterTop = visibleListOffset(m.chapterTop, m.chapter, len(m.page.Chapters), m.visibleRows())
		if m.help {
			m.scrollHelp("")
		}
		if m.jumping {
			m.jumpInput.SetWidth(max(1, m.width-3))
		}
	case previewGraphicsMsg:
		if m.graphics != bool(v) {
			m.graphics = bool(v)
			m.upgradeImage = false
			if m.graphics && !m.textOnly && strings.EqualFold(filepath.Ext(m.path), ".pdf") && (m.busy || m.page.Image == nil) {
				if m.busy {
					// Finish the active extraction. The image upgrade then
					// reuses its page count and text.
					m.upgradeImage = true
					return m, nil
				}
				return m.load(m.index)
			}
		}
	case previewLoadedMsg:
		if v.generation != m.generation {
			return m, nil
		}
		m.busy = false
		upgrade := m.upgradeImage && v.page.Image == nil
		m.upgradeImage = false
		if v.err != nil {
			if upgrade {
				return m.load(m.index)
			}
			m.notice = bookCell(safeText(v.err.Error()))
			// Keep the last good page and its navigation after a failed page turn.
			if m.page.Count > 0 {
				m.index = m.page.Index
			}
			return m, nil
		}
		if m.zoomPercent > 100 && m.page.Count > 0 && m.page.Index != v.page.Index {
			m.panX, m.panY = 0, -100 // Start the next enlarged page at the top.
		}
		m.page, m.index, m.notice = v.page, v.page.Index, v.historyWarning
		m.wrap()
		if upgrade {
			m.busy = true
			m.generation++
			return m, m.Init()
		}
	case libraryOpenedMsg:
		m.notice = v.message()
	case tea.MouseWheelMsg:
		return m.wheel(v)
	case tea.KeyPressMsg:
		if m.jumping {
			return m.updateJump(v)
		}
		return m.key(v.String())
	default:
		if m.jumping {
			return m.updateJump(msg)
		}
	}
	return m, nil
}

func (m previewPageModel) wheel(event tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	if m.jumping || m.height < 7 || (m.page.Count == 0 && !m.help) {
		return m, nil
	}
	var step int
	switch event.Button {
	case tea.MouseWheelUp:
		step = -1
	case tea.MouseWheelDown:
		step = 1
	default:
		return m, nil
	}
	switch event.Mod {
	case 0:
		if m.help {
			if step > 0 {
				m.scrollHelp("down")
			} else {
				m.scrollHelp("up")
			}
			return m, nil
		}
		if m.contents {
			m.chapter = max(0, min(len(m.page.Chapters)-1, m.chapter+step))
			m.chapterTop = visibleListOffset(m.chapterTop, m.chapter, len(m.page.Chapters), m.visibleRows())
			return m, nil
		}
		return m.turn(step)
	case tea.ModCtrl:
		if !m.help && m.showingImage() {
			m.setZoom(m.zoomPercent - 25*step)
		}
	}
	return m, nil
}

func (m previewPageModel) turn(step int) (tea.Model, tea.Cmd) {
	index := m.index + step
	if index < 0 || index >= m.page.Count {
		return m, nil
	}
	return m.load(index)
}

func (m previewPageModel) key(k string) (tea.Model, tea.Cmd) {
	if m.help {
		switch k {
		case "?", "esc", "q", "ctrl+c":
			m.help = false
		default:
			m.scrollHelp(k)
		}
		return m, nil
	}
	switch k {
	case "?":
		m.help, m.helpOffset = true, 0
		return m, nil
	case "g":
		if !m.busy && m.page.Count > 0 {
			return m.openJump()
		}
		return m, nil
	}
	if m.contents {
		switch k {
		case "esc", "c", "q", "ctrl+c":
			m.contents = false
		case "enter":
			if m.chapter != m.index {
				return m.load(m.chapter)
			}
			m.contents = false
		default:
			m.chapter = listCursor(k, m.chapter, len(m.page.Chapters), m.visibleRows())
			m.chapterTop = visibleListOffset(m.chapterTop, m.chapter, len(m.page.Chapters), m.visibleRows())
		}
		return m, nil
	}
	switch k {
	case "q", "esc", "ctrl+c":
		m.cancel()
		return m, tea.Quit
	case "o":
		return m, libraryOpenCmd(m.path)
	case " ", "space":
		if !m.busy {
			if m.nextTextScreen() {
				m.offset = min(len(m.lines)-m.visibleRows(), m.offset+m.visibleRows())
			} else {
				return m.turn(1)
			}
		}
	case "right", "l":
		return m.turn(1)
	case "left", "h":
		return m.turn(-1)
	case "c":
		if !m.busy && len(m.page.Chapters) > 0 {
			m.contents, m.chapter = true, m.index
			m.chapterTop = visibleListOffset(0, m.chapter, len(m.page.Chapters), m.visibleRows())
		}
	case "x":
		if !m.busy && m.graphics && strings.EqualFold(filepath.Ext(m.path), ".pdf") {
			m.textOnly = !m.textOnly
			if !m.textOnly && m.page.Image == nil {
				return m.load(m.index)
			}
		}
	default:
		if m.showingImage() {
			m.imageKey(k)
		} else if !m.busy {
			m.offset = listCursor(k, m.offset, max(1, len(m.lines)-m.visibleRows()+1), m.visibleRows())
		}
	}
	return m, nil
}

func (m *previewPageModel) setZoom(percent int) {
	m.zoomPercent = max(minPageZoom, min(maxPageZoom, percent))
	if m.zoomPercent <= 100 {
		m.panX, m.panY = 0, 0
	}
}

func (m *previewPageModel) imageKey(k string) {
	switch k {
	case "+", "=":
		m.setZoom(m.zoomPercent + 25)
	case "-":
		m.setZoom(m.zoomPercent - 25)
	case "0":
		m.setZoom(100)
	default:
		if m.zoomPercent <= 100 {
			return
		}
		switch k {
		case "up":
			m.panY = max(-100, m.panY-25)
		case "down":
			m.panY = min(100, m.panY+25)
		case "shift+left":
			m.panX = max(-100, m.panX-25)
		case "shift+right":
			m.panX = min(100, m.panX+25)
		}
	}
}

func (m previewPageModel) showingImage() bool {
	return m.graphics && !m.busy && !m.contents && !m.textOnly && m.page.Image != nil && m.width >= 4 && m.height >= 8
}

func (m previewPageModel) coverSlots() []coverSource {
	if m.help || m.jumping || !m.showingImage() {
		return nil
	}
	// screenView starts the body at row 3 and leaves its last three rows for controls.
	area := image.Rect(1, 3, m.width-1, m.height-3)
	return []coverSource{{
		url: fmt.Sprintf("page:%s/%d/%d", m.path, m.index, m.generation), img: m.page.Image,
		width: m.width, height: m.height, area: area, documentPage: true,
		zoomPercent: m.zoomPercent, panX: m.panX, panY: m.panY,
	}}
}

func (m previewPageModel) View() tea.View {
	if m.help {
		return m.helpView()
	}
	if m.jumping {
		return m.jumpView()
	}
	title := "Preview"
	subtitle := bookCell(safeText(filepath.Base(m.path)))
	body := ""
	status := m.notice
	if m.contents {
		rows := make([]string, 0, m.visibleRows())
		for i := m.chapterTop; i < min(len(m.page.Chapters), m.chapterTop+m.visibleRows()); i++ {
			rows = append(rows, selectionRow(fmt.Sprintf("%d. %s", i+1, safeText(m.page.Chapters[i])), i == m.chapter, m.width))
		}
		return m.previewView("Chapters", subtitle, strings.Join(rows, "\n"), "", m.previewHints()...)
	}
	if m.busy {
		body = "Loading preview…"
	} else if m.page.Count > 0 {
		unit := "Page"
		if len(m.page.Chapters) > 0 {
			unit = "Chapter"
		}
		title = fmt.Sprintf("%s %d of %d", unit, m.index+1, m.page.Count)
		if m.page.Title != "" {
			subtitle = bookCell(safeText(m.page.Title))
		}
		if len(m.lines) > 0 {
			body = strings.Join(m.lines[m.offset:min(len(m.lines), m.offset+m.visibleRows())], "\n")
		}
		if status == "" && !m.showingImage() && len(m.lines) > m.visibleRows() {
			status = fmt.Sprintf("Lines %d–%d of %d", m.offset+1, min(len(m.lines), m.offset+m.visibleRows()), len(m.lines))
		}
	} else if status != "" {
		body = ansi.Hardwrap(status, max(1, m.width), true)
		status = ""
	}
	if m.showingImage() {
		title += fmt.Sprintf(" · %d%%", m.zoomPercent)
		if m.zoomPercent > 100 && status == "" {
			status = "↑/↓ pan vertically · shift+←/→ pan horizontally"
		}
	}
	return m.previewView(title, subtitle, body, status, m.previewHints()...)
}

func (m previewPageModel) previewView(title, subtitle, body, status string, hints ...keys.Binding) tea.View {
	view := screenView(m.width, m.height, title, subtitle, body, status, hints...)
	if (m.page.Count > 0 || m.help) && m.height >= 7 {
		view.MouseMode = tea.MouseModeCellMotion
	}
	return view
}
