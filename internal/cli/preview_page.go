package cli

import (
	"context"
	"fmt"
	"image"
	"path/filepath"
	"strings"

	keys "charm.land/bubbles/v2/key"
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
	lines                                    []string
	busy, graphics, textOnly, contents       bool
	chapter, chapterTop                      int
	zoomPercent, panX, panY                  int
	notice                                   string
	ctx                                      context.Context
	cancel                                   context.CancelFunc
}

func newPreviewPage(path string) previewPageModel {
	ctx, cancel := context.WithCancel(context.Background())
	return previewPageModel{path: path, width: 80, height: 24, zoomPercent: 100, busy: true, graphics: coverGraphicsAvailable.Load(), ctx: ctx, cancel: cancel}
}

func (m previewPageModel) Init() tea.Cmd {
	return func() tea.Msg {
		page, err := preview.Load(m.ctx, m.path, m.index, m.graphics && !m.textOnly)
		result := previewLoadedMsg{generation: m.generation, page: page, err: err}
		if err == nil && m.page.Count == 0 && m.ctx.Err() == nil {
			if err := recordBookOpened(m.path); err != nil {
				result.historyWarning = "Could not update reading history: " + bookCell(safeText(err.Error()))
			}
		}
		return result
	}
}

func (m previewPageModel) load(index int) (tea.Model, tea.Cmd) {
	m.cancel()
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.generation++
	m.index, m.offset, m.busy, m.contents, m.notice = index, 0, true, false, ""
	return m, m.Init()
}

func (m previewPageModel) visibleRows() int { return max(1, m.height-6) }

func (m *previewPageModel) wrap() {
	m.lines = strings.Split(ansi.Hardwrap(safeText(ansi.Strip(m.page.Text)), max(1, m.width), true), "\n")
	m.offset = min(m.offset, max(0, len(m.lines)-m.visibleRows()))
}

func (m previewPageModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		m.wrap()
		m.chapterTop = visibleListOffset(m.chapterTop, m.chapter, len(m.page.Chapters), m.visibleRows())
	case previewGraphicsMsg:
		if m.graphics != bool(v) {
			m.graphics = bool(v)
			if m.graphics && !m.textOnly && strings.EqualFold(filepath.Ext(m.path), ".pdf") && (m.busy || m.page.Image == nil) {
				return m.load(m.index)
			}
		}
	case previewLoadedMsg:
		if v.generation != m.generation {
			return m, nil
		}
		m.busy = false
		if v.err != nil {
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
	case libraryOpenedMsg:
		m.notice = v.message()
	case tea.MouseWheelMsg:
		if m.showingImage() {
			switch v.Button {
			case tea.MouseWheelUp:
				m.setZoom(m.zoomPercent + 25)
			case tea.MouseWheelDown:
				m.setZoom(m.zoomPercent - 25)
			}
		}
	case tea.KeyPressMsg:
		return m.key(v.String())
	}
	return m, nil
}

func (m previewPageModel) key(k string) (tea.Model, tea.Cmd) {
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
	case "right", "l":
		if m.index+1 < m.page.Count {
			return m.load(m.index + 1)
		}
	case "left", "h":
		if m.index > 0 {
			return m.load(m.index - 1)
		}
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
	if !m.showingImage() {
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
	title := "Preview"
	subtitle := bookCell(safeText(filepath.Base(m.path)))
	body := ""
	status := m.notice
	if m.contents {
		rows := make([]string, 0, m.visibleRows())
		for i := m.chapterTop; i < min(len(m.page.Chapters), m.chapterTop+m.visibleRows()); i++ {
			rows = append(rows, selectionRow(fmt.Sprintf("%d. %s", i+1, safeText(m.page.Chapters[i])), i == m.chapter, m.width))
		}
		return screenView(m.width, m.height, "Chapters", subtitle, strings.Join(rows, "\n"), "", uiKey("↑/↓", "select"), uiKey("enter", "read"), uiKey("esc", "back"))
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
	hints := []keys.Binding{uiKey("←/→", "turn"), uiKey("↑/↓", "scroll")}
	if m.showingImage() {
		title += fmt.Sprintf(" · %d%%", m.zoomPercent)
		hints = []keys.Binding{uiKey("wheel/+/-", "zoom"), uiKey("0", "fit"), uiKey("←/→", "turn")}
		if m.zoomPercent > 100 && status == "" {
			status = "↑/↓ pan vertically · shift+←/→ pan horizontally"
		}
	}
	if len(m.page.Chapters) > 0 {
		hints = append(hints, uiKey("c", "chapters"))
	}
	if m.graphics && strings.EqualFold(filepath.Ext(m.path), ".pdf") {
		hints = append(hints, uiKey("x", "text/image"))
	}
	hints = append(hints, uiKey("o", "open"), uiKey("esc", "back"))
	view := screenView(m.width, m.height, title, subtitle, body, status, hints...)
	if m.showingImage() {
		view.MouseMode = tea.MouseModeCellMotion
	}
	return view
}
