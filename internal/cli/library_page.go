package cli

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/heartleo/zlib/internal/library"
)

type previewRequestedMsg struct{ path string }

type shelfLoadedMsg struct {
	books    []library.Book
	selected int64
	notice   string
	err      error
}

type libraryPageModel struct {
	dbPath, query, notice                 string
	width, height, cursor, top            int
	books                                 []library.Book
	busy, confirming, confirmRemove, help bool
	editor                                metaEditor
	field                                 string
}

func newLibraryPage(dbPath string) libraryPageModel {
	return libraryPageModel{dbPath: dbPath, width: 80, height: 24, busy: true}
}

func (m libraryPageModel) Init() tea.Cmd { return m.load(nil) }

// Each command owns its connection, including when a reader leaves mid-scan.
func (m libraryPageModel) load(change func(*library.Store) (string, error)) tea.Cmd {
	path, query := m.dbPath, m.query
	var selected int64
	if b := m.selected(); b != nil {
		selected = b.ID
	}
	return func() tea.Msg {
		result := shelfLoadedMsg{selected: selected}
		store, err := library.Open(path)
		if err != nil {
			result.err = err
			return result
		}
		defer store.Close()
		if change != nil {
			result.notice, result.err = change(store)
			if result.err != nil {
				return result
			}
		}
		result.books, result.err = store.List(query)
		return result
	}
}

func (m libraryPageModel) selected() *library.Book {
	if m.cursor >= 0 && m.cursor < len(m.books) {
		return &m.books[m.cursor]
	}
	return nil
}

func (m libraryPageModel) visibleRows() int { return max(1, m.height-10) }

func (m libraryPageModel) edit(field, title, value string) (tea.Model, tea.Cmd) {
	m.field, m.notice = field, ""
	var cmd tea.Cmd
	m.editor, cmd = newMetaEditor(title, []string{title}, []string{value}, m.width)
	return m, cmd
}

func (m libraryPageModel) submit() (tea.Model, tea.Cmd) {
	value := strings.TrimSpace(m.editor.values()[0])
	var change func(*library.Store) (string, error)
	switch m.field {
	case "search":
		m.query, m.cursor, m.top = value, 0, 0
	case "tags":
		b := m.selected()
		if b == nil {
			m.field = ""
			return m, nil
		}
		id := b.ID
		change = func(s *library.Store) (string, error) {
			return "Tags saved", s.SetTags(id, strings.Split(value, ","))
		}
	case "add", "scan":
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}
		p, err := expandTilde(value)
		if err != nil || p == "" {
			m.editor.err = "Enter a path"
			return m, nil
		}
		if m.field == "add" {
			change = func(s *library.Store) (string, error) {
				_, err := s.Add(p)
				return "Book added", err
			}
		} else {
			change = func(s *library.Store) (string, error) {
				n, err := s.Scan(p)
				return fmt.Sprintf("%d files indexed", n), err
			}
		}
	}
	m.busy = true
	return m, m.load(change)
}

func (m libraryPageModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		m.editor.resize(m.width)
		m.top = visibleListOffset(m.top, m.cursor, len(m.books), m.visibleRows())
		return m, nil
	case shelfLoadedMsg:
		m.busy = false
		if v.err != nil {
			m.notice = bookCell(safeText(v.err.Error()))
			if m.field != "" {
				m.editor.err = m.notice
			}
			return m, nil
		}
		m.books, m.field, m.notice = v.books, "", v.notice
		if i := slices.IndexFunc(m.books, func(b library.Book) bool { return b.ID == v.selected }); i >= 0 {
			m.cursor = i
		}
		m.cursor = min(m.cursor, max(0, len(m.books)-1))
		m.top = visibleListOffset(m.top, m.cursor, len(m.books), m.visibleRows())
		return m, nil
	}
	key, pressed := msg.(tea.KeyPressMsg)
	if m.busy {
		if pressed && (key.String() == "esc" || key.String() == "ctrl+c") {
			return m, tea.Quit
		}
		return m, nil
	}
	if m.field != "" {
		if pressed {
			switch key.String() {
			case "esc", "ctrl+c":
				m.field, m.notice = "", ""
				return m, nil
			case "enter":
				return m.submit()
			}
		}
		var cmd tea.Cmd
		m.editor, cmd = m.editor.update(msg)
		return m, cmd
	}
	if !pressed {
		return m, nil
	}
	return m.key(key.String())
}

func (m libraryPageModel) key(k string) (tea.Model, tea.Cmd) {
	b := m.selected()
	if m.confirming {
		switch k {
		case "left", "right", "tab", "shift+tab":
			m.confirmRemove = !m.confirmRemove
		case "esc", "n", "ctrl+c":
			m.confirming = false
		case "enter":
			m.confirming = false
			if m.confirmRemove && b != nil {
				id := b.ID
				m.busy = true
				return m, m.load(func(s *library.Store) (string, error) { return "Entry removed", s.Remove(id) })
			}
		}
		return m, nil
	}
	if m.help {
		m.help = false
		return m, nil
	}
	switch k {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.help = true
	case "a":
		return m.edit("add", "Add a book file", "")
	case "s":
		return m.edit("scan", "Scan a directory", "")
	case "f", "/":
		return m.edit("search", "Find books", m.query)
	case "enter", "p", "o", "d", "r", "t":
		if b == nil {
			return m, nil
		}
		switch k {
		case "t":
			return m.edit("tags", "Tags (comma-separated)", strings.Join(b.Tags, ", "))
		case "d":
			m.confirming, m.confirmRemove = true, false
		case "r":
			status := "unread"
			switch b.Status {
			case "unread":
				status = "reading"
			case "reading":
				status = "read"
			}
			id := b.ID
			m.busy = true
			return m, m.load(func(s *library.Store) (string, error) { return "Marked " + status, s.SetStatus(id, status) })
		default:
			if b.Missing {
				m.notice = "File missing. Restore it and scan its directory again."
				return m, nil
			}
			path := b.Path
			if k == "o" {
				m.busy = true
				return m, m.load(func(*library.Store) (string, error) {
					return "Requested the system reader", openLibraryFile(path)
				})
			}
			if !canPreviewBook(path) {
				m.notice = "Use o to open this format in your reader"
				return m, nil
			}
			return m, func() tea.Msg { return previewRequestedMsg{path: path} }
		}
	default:
		m.cursor = listCursor(k, m.cursor, len(m.books), m.visibleRows())
		m.top = visibleListOffset(m.top, m.cursor, len(m.books), m.visibleRows())
	}
	return m, nil
}

type libraryOpenedMsg struct{ err error }

func (m libraryOpenedMsg) message() string {
	if m.err != nil {
		return bookCell(safeText(m.err.Error()))
	}
	return "Requested the system reader"
}

func libraryOpenCmd(path string) tea.Cmd {
	return func() tea.Msg { return libraryOpenedMsg{openLibraryFile(path)} }
}

func (m libraryPageModel) View() tea.View {
	if m.help {
		body := strings.Join([]string{
			"↑/↓    Select a book", "Enter  Preview PDF or EPUB", "o      Open in the system reader",
			"f      Find books", "a      Add a file", "s      Scan a directory",
			"r      Cycle unread / reading / read", "t      Edit tags", "d      Remove from the bookshelf",
		}, "\n")
		return screenView(m.width, m.height, "Bookshelf shortcuts", "", body, "", uiKey("esc", "back"))
	}
	if m.field != "" {
		body := m.editor.view(m.width)
		return screenView(m.width, m.height, "Local bookshelf", "", body, m.activity(), uiKey("enter", "apply"), uiKey("esc", "cancel"))
	}
	subtitle := fmt.Sprintf("%d books", len(m.books))
	if len(m.books) == 1 {
		subtitle = "1 book"
	}
	if m.query != "" {
		subtitle += " matching " + bookCell(safeText(m.query))
	}
	var rows []string
	if b := m.selected(); b != nil {
		rows = append(rows, colorBold(bookCell(safeText(b.Title))), bookCell(safeText(tildePath(b.Path))))
		detail := strings.ToUpper(b.Format) + " · " + b.Status
		if b.Missing {
			detail += " · missing"
		}
		if len(b.Tags) > 0 {
			detail += " · " + strings.Join(b.Tags, ", ")
		}
		rows = append(rows, colorFaint(bookCell(safeText(detail))), "")
		for i := m.top; i < min(len(m.books), m.top+m.visibleRows()); i++ {
			book := m.books[i]
			label := book.Title
			if label == "" {
				label = filepath.Base(book.Path)
			}
			if book.Authors != "" {
				label += " — " + book.Authors
			}
			rows = append(rows, selectionRow(safeText(label), i == m.cursor, m.width))
		}
	} else if m.busy {
		rows = append(rows, "Loading bookshelf…")
	} else if m.query != "" {
		rows = append(rows, "No matching books. Press f to change the search.")
	} else {
		rows = append(rows, "Press a to add a book or s to scan a directory.")
	}
	v := screenView(m.width, m.height, "Local bookshelf", subtitle, strings.Join(rows, "\n"), m.activity(),
		uiKey("enter", "preview"), uiKey("f", "find"), uiKey("a", "add"), uiKey("s", "scan"), uiKey("?", "keys"), uiKey("esc", "back"))
	if m.confirming {
		if b := m.selected(); b != nil {
			v.Content = confirmationDialog(v.Content, m.width, m.height, "Remove from bookshelf?", bookCell(safeText(b.Title)), "", "The book file stays on disk.", "Remove", m.confirmRemove, true)
		}
	}
	return v
}

func (m libraryPageModel) activity() string {
	if m.busy {
		return "Updating bookshelf…"
	}
	if m.notice == "" && m.field == "" && m.selected() != nil {
		return "o open  ·  r reading status  ·  t tags  ·  d remove"
	}
	return ansi.Truncate(m.notice, max(1, m.width), "…")
}
