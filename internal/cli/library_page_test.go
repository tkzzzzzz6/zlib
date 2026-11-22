package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Exercise the page commands against the same on-disk bookshelf as the CLI.
func TestLibraryPageManagePreviewAndRemove(t *testing.T) {
	file := writeEPUB(t, "Field Guide", "<p>First page.</p>")
	m := newLibraryPage(filepath.Join(t.TempDir(), "library.db"))
	update := func(msg tea.Msg) tea.Cmd {
		t.Helper()
		next, cmd := m.Update(msg)
		m = next.(libraryPageModel)
		return cmd
	}
	update(m.Init()())
	if !strings.Contains(m.View().Content, "add a book") {
		t.Fatal("empty bookshelf omitted its actions")
	}
	edit := func(k, value string) {
		t.Helper()
		update(key(k))
		update(tea.PasteMsg{Content: value})
		cmd := update(key("enter"))
		if cmd == nil {
			t.Fatalf("missing command for %s", k)
		}
		update(cmd())
		if m.field != "" {
			t.Fatalf("edit failed: %s", m.notice)
		}
	}
	edit("s", filepath.Dir(file))
	if len(m.books) != 1 || m.books[0].Title != "Field Guide" {
		t.Fatalf("scan: %+v", m.books)
	}
	edit("t", "reference, botany")
	if !slices.Contains(m.books[0].Tags, "botany") {
		t.Fatalf("tags: %v", m.books[0].Tags)
	}
	cmd := update(key("r"))
	update(cmd())
	if m.books[0].Status != "reading" {
		t.Fatalf("status: %s", m.books[0].Status)
	}
	edit("f", "botany")
	if len(m.books) != 1 {
		t.Fatal("tag search lost the book")
	}
	if got := update(key("enter"))().(previewRequestedMsg).path; got != file {
		t.Fatalf("preview path %q", got)
	}
	update(key("d"))
	if !strings.Contains(ansi.Strip(m.View().Content), "file stays on disk") {
		t.Fatal("removal omitted file disposition")
	}
	if cmd := update(key("enter")); cmd != nil || m.confirming || len(m.books) != 1 {
		t.Fatal("Enter should cancel by default")
	}
	update(key("d"))
	update(tea.KeyPressMsg{Code: tea.KeyRight})
	cmd = update(key("enter"))
	update(cmd())
	if len(m.books) != 0 {
		t.Fatal("entry still on the shelf")
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
}

func TestLibraryPageEditorOwnsKeysAndErrors(t *testing.T) {
	m := newLibraryPage(filepath.Join(t.TempDir(), "library.db"))
	next, _ := m.Update(m.Init()())
	next, _ = next.Update(key("a"))
	next, _ = next.Update(key("q"))
	if got := next.(libraryPageModel).editor.values()[0]; got != "q" {
		t.Fatalf("typed q = %q", got)
	}
	next, cmd := next.Update(key("enter"))
	next, _ = next.Update(cmd())
	m = next.(libraryPageModel)
	if m.field != "add" || m.editor.err == "" {
		t.Fatal("bad path should remain editable")
	}
	next, _ = next.Update(key("esc"))
	if next.(libraryPageModel).field != "" {
		t.Fatal("Esc did not close editor")
	}
}
