package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// fileModelStep feeds msg to m and runs any directory read it starts.
func fileModelStep(t *testing.T, m sendFileModel, msg tea.Msg) sendFileModel {
	t.Helper()
	next, cmd := m.Update(msg)
	m = next.(sendFileModel)
	if cmd != nil {
		if read, ok := cmd().(bookFileListMsg); ok {
			next, _ = m.Update(read)
			m = next.(sendFileModel)
		}
	}
	return m
}

func bookDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		path := filepath.Join(dir, name)
		if strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestWeReadFilePickerListsOnlySupportedBooks(t *testing.T) {
	dir := bookDir(t, "Dune.epub", "NOTES.TXT", "paper.pdf", "cover.jpg", "draft.docx", "readme", "books/")
	m := newWeReadFileModel(dir)
	m = fileModelStep(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = fileModelStep(t, m, m.Init()())

	var names []string
	for _, e := range m.files.entries {
		names = append(names, e.name)
	}
	if got, want := strings.Join(names, ","), "books,Dune.epub,NOTES.TXT,paper.pdf"; got != want {
		t.Fatalf("entries = %s, want %s", got, want)
	}
	screen := ansi.Strip(m.View().Content)
	for _, hidden := range []string{"cover.jpg", "draft.docx", "readme"} {
		if strings.Contains(screen, hidden) {
			t.Fatalf("picker shows unsupported %s:\n%s", hidden, screen)
		}
	}
	if !strings.Contains(screen, "books"+string(filepath.Separator)) || !strings.Contains(screen, "Showing folders and EPUB, PDF, MOBI, AZW3, TXT files") {
		t.Fatalf("picker lacks the folder or the format line:\n%s", screen)
	}

	// Every file row is selectable: Enter on the first file confirms it.
	m = fileModelStep(t, m, key("down"))
	m = fileModelStep(t, m, key("enter"))
	if !m.confirm || m.path != filepath.Join(dir, "Dune.epub") {
		t.Fatalf("confirm = %v, path = %q", m.confirm, m.path)
	}
}

func TestWeReadFilePickerNavigatesFolders(t *testing.T) {
	dir := bookDir(t, "a/", "b/", "b/inner.mobi", "b/skip.zip")
	m := newWeReadFileModel(dir)
	m = fileModelStep(t, m, m.Init()())
	m = fileModelStep(t, m, key("down"))
	m = fileModelStep(t, m, key("enter"))
	if m.files.dir != filepath.Join(dir, "b") || len(m.files.entries) != 1 || m.files.entries[0].name != "inner.mobi" {
		t.Fatalf("inside b: dir %q, entries %+v", m.files.dir, m.files.entries)
	}
	if screen := ansi.Strip(m.View().Content); strings.Contains(screen, "skip.zip") {
		t.Fatalf("unsupported file shown:\n%s", screen)
	}

	m = fileModelStep(t, m, key("left"))
	if m.files.dir != dir || m.files.entries[m.files.cursor].name != "b" {
		t.Fatalf("parent: dir %q, cursor on %q, want back on b", m.files.dir, m.files.entries[m.files.cursor].name)
	}

	// A read for a folder already left must not replace the current list.
	stale := bookFileListMsg{dir: filepath.Join(dir, "a"), entries: []bookFileEntry{{name: "late.epub"}}}
	next, _ := m.Update(stale)
	if got := next.(sendFileModel); len(got.files.entries) != 2 {
		t.Fatalf("stale read replaced entries: %+v", got.files.entries)
	}
}

func TestWeReadFilePickerEmptyFolderSaysWhy(t *testing.T) {
	m := newWeReadFileModel(bookDir(t, "image.png"))
	m = fileModelStep(t, m, m.Init()())
	if screen := ansi.Strip(m.View().Content); !strings.Contains(screen, "No supported files here") {
		t.Fatalf("empty folder:\n%s", screen)
	}
	m = fileModelStep(t, m, key("enter"))
	if m.confirm {
		t.Fatal("Enter in an empty folder opened a confirmation")
	}
}

func TestShellPreviewFilePickerActions(t *testing.T) {
	dir := bookDir(t, "a.pdf", "b.epub")
	t.Setenv(downloadDirEnvVar, dir)
	for _, size := range [][2]int{{80, 24}, {40, 16}, {24, 10}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := newShellModel()
			m, _ = shellStep(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			cmd := m.dispatch("/preview")
			m, _ = shellStep(m, cmd())
			m, _ = shellStep(m, key("down"))
			m, _ = shellStep(m, key("enter"))
			screen := ansi.Strip(m.View().Content)
			open := strings.Index(screen, "Open with default app")
			if preview, cancel := strings.Index(screen, "Preview"), strings.LastIndex(screen, "Cancel"); preview < 0 || open <= preview || cancel <= open {
				t.Fatalf("preview choices are missing or out of order:\n%s", screen)
			}
			for _, line := range strings.Split(screen, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("preview choices overflow: %q", line)
				}
			}

			m, _ = shellStep(m, key("left"))
			m, cmd = shellStep(m, key("enter"))
			if picker := m.page.(sendFileModel); cmd != nil || picker.confirm || picker.files.cursor != 1 {
				t.Fatal("Cancel should dismiss the choices and retain the file")
			}

			m, _ = shellStep(m, key("enter"))
			m, _ = shellStep(m, key("right"))
			m, cmd = shellStep(m, key("enter"))
			if cmd == nil {
				t.Fatal("Open with default app did not request a launch")
			}
			// The fixture is not a valid EPUB. The normal launcher rejects it
			// before invoking the system app, keeping this test offline.
			event := cmd().(shellEvent)
			if result, ok := event.msg.(previewFileOpenedMsg); !ok || result.result.err == nil {
				t.Fatalf("expected a failed open, got %#v", event.msg)
			}
			m, _ = shellStep(m, event)
			if picker := m.page.(sendFileModel); picker.confirm || picker.files.cursor != 1 || !strings.Contains(picker.message, "could not recognize this file") {
				t.Fatalf("failed open did not return to the selected file: %q", picker.message)
			}

			m, _ = shellStep(m, key("enter"))
			m, cmd = shellStep(m, key("esc"))
			if cmd != nil || m.page.(sendFileModel).confirm {
				t.Fatal("Esc should close the choices without opening anything")
			}
			m, _ = shellStep(m, key("enter"))
			m, cmd = shellStep(m, key("enter"))
			m, _ = shellStep(m, cmd())
			if page, ok := m.page.(previewPageModel); !ok || page.path != filepath.Join(dir, "b.epub") {
				t.Fatal("default action did not enter the preview for the selected file")
			}
		})
	}
}

func TestPreviewPickerIgnoresOlderOpenResult(t *testing.T) {
	m := newSendFileModel("", previewFileTarget)
	m.openRequest = 2
	m = fileModelStep(t, m, previewFileOpenedMsg{2, libraryOpenedMsg{}})
	m = fileModelStep(t, m, previewFileOpenedMsg{1, libraryOpenedMsg{fmt.Errorf("older failure")}})
	if m.message != (libraryOpenedMsg{}).message() {
		t.Fatalf("an older open result replaced the latest status: %s", m.message)
	}
}
