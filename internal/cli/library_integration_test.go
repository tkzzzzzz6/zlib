package cli

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/heartleo/zlib/internal/library"
	"github.com/heartleo/zlib/internal/zlib"
)

func TestSavedBookJoinsRegisteredLibrary(t *testing.T) {
	isolateConfigHome(t)
	dir := t.TempDir()
	book, err := os.ReadFile(writeTestEPUB(t))
	if err != nil {
		t.Fatal(err)
	}
	server := fakeEAPI(t, "Before", book)
	client := zlib.NewClient(zlib.WithDomain(server.URL))
	client.SetCookies(map[string]string{"remix_userid": "1", "remix_userkey": "fixture"})
	dbPath, err := library.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if warning := libraryDownloadWarning(filepath.Join(dir, "book.epub")); warning != "" {
		t.Fatal(warning)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("an ordinary download should not create a bookshelf: %v", err)
	}
	store, err := library.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Scan(dir); err != nil {
		t.Fatal(err)
	}
	result, err := newDownloadModel("42:abc", dir, client).runPlain(io.Discard)
	if err != nil || result.libraryWarning != "" {
		t.Fatalf("download: %v, %s", err, result.libraryWarning)
	}
	books, err := store.List("")
	if err != nil || len(books) != 1 || books[0].Title != "Before" {
		t.Fatalf("registered books: %+v, %v", books, err)
	}
	if err := store.SetStatus(books[0].ID, "reading"); err != nil {
		t.Fatal(err)
	}
	m := metaPageModel{path: result.savedPath}
	next, _ := m.renameTo("renamed.epub")
	if next.(metaPageModel).path == result.savedPath {
		t.Fatal("rename did not update the path")
	}
	after, err := store.List("renamed.epub")
	if err != nil || len(after) != 1 || after[0].ID != books[0].ID || after[0].Status != "reading" {
		t.Fatalf("renamed entry: %+v, %v", after, err)
	}
}

func TestShellPreviewReturnsToSavedBook(t *testing.T) {
	isolateConfigHome(t)
	d := newDownloadModel("42:abc", "", nil)
	d.state, d.savedPath = stateComplete, writeTestEPUB(t)
	d.completeAction = slices.Index(d.completeActions(), actionPreview)
	if d.completeAction < 0 {
		t.Fatal("missing Preview action")
	}
	m := newShellModel()
	m.open("download", d)
	m, cmd := shellStep(m, key("enter"))
	m, _ = shellStep(m, cmd())
	if m.kind != "preview" || m.page.(previewPageModel).path != d.savedPath {
		t.Fatalf("opened %s", m.kind)
	}
	m, cmd = shellStep(m, key("esc"))
	m, _ = shellStep(m, cmd())
	if m.kind != "download" || m.page.(downloadModel).savedPath != d.savedPath {
		t.Fatalf("returned to %s", m.kind)
	}
}

func TestLibraryPreviewRestoresFilteredSelection(t *testing.T) {
	isolateConfigHome(t)
	m, err := newStandaloneShellCommand("/library")
	if err != nil {
		t.Fatal(err)
	}
	path := writeTestEPUB(t)
	page := m.page.(libraryPageModel)
	page.query = "Before"
	m.page = page
	m, _ = shellStep(m, shellEvent{m.generation, previewRequestedMsg{path}})
	if m.kind != "preview" {
		t.Fatalf("opened %s", m.kind)
	}
	m, cmd := shellStep(m, key("esc"))
	m, _ = shellStep(m, cmd())
	if m.kind != "library" || m.page.(libraryPageModel).query != "Before" {
		t.Fatalf("returned to %s", m.kind)
	}
}
