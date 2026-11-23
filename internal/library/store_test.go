package library

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/heartleo/zlib/internal/ebookmeta"
)

const fixtureFB2 = `<FictionBook><description><title-info><book-title>Reference</book-title>
<author><nickname>Reader</nickname></author></title-info></description></FictionBook>`

func testFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func testList(t *testing.T, s *Store, query string, count int) []Book {
	t.Helper()
	books, err := s.List(query)
	if err != nil || len(books) != count {
		t.Fatalf("List(%q) = %+v, %v; want %d books", query, books, err, count)
	}
	return books
}

func testScan(t *testing.T, s *Store, dir string, count int) {
	t.Helper()
	if n, err := s.Scan(dir); n != count || err != nil {
		t.Fatalf("Scan = %d, %v; want %d", n, err, count)
	}
}

func TestBooksAndAnnotationsSurviveRefreshRenameAndRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db with # and %", "library.db")
	dir := t.TempDir()
	path := testFile(t, dir, "draft.fb2", fixtureFB2)
	s := openTestStore(t, dbPath)
	b, err := s.Add(path)
	if err != nil || b.ID == 0 || b.Title != "Reference" || b.Authors != "Reader" || b.Format != "fb2" || b.Status != "unread" {
		t.Fatalf("Add = %+v, %v", b, err)
	}
	if err := s.SetStatus(b.ID, "reading"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTags(b.ID, []string{"  Étude ", "étude", ""}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(b.ID, "bad"); err == nil {
		t.Fatal("accepted unknown status")
	}
	testScan(t, s, dir, 1)
	moved := filepath.Join(dir, "edited.fb2")
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err := s.Relocate(path, moved); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, dbPath)
	current := testList(t, s, "ÉTUDE", 1)[0]
	if current.ID != b.ID || filepath.Base(current.Path) != "edited.fb2" || current.Status != "reading" || !slices.Equal(current.Tags, []string{"Étude"}) {
		t.Fatalf("reopened book = %+v", current)
	}
	if err := os.Remove(moved); err != nil {
		t.Fatal(err)
	}
	testScan(t, s, dir, 0)
	if !testList(t, s, "reader", 1)[0].Missing {
		t.Fatal("missing file still marked available")
	}
	testFile(t, dir, "edited.fb2", fixtureFB2)
	testScan(t, s, dir, 1)
	b = testList(t, s, "", 1)[0]
	if b.Missing || b.ID != current.ID || b.Status != "reading" {
		t.Fatalf("reappeared file = %+v", b)
	}
	if err := s.Remove(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatal("removal touched the file:", err)
	}
	testScan(t, s, dir, 0)
	testList(t, s, "", 0)
	b, err = s.Add(moved)
	if err != nil || b.ID == current.ID || b.Status != "unread" || len(b.Tags) != 0 {
		t.Fatalf("explicit re-add = %+v, %v", b, err)
	}
}

func TestScanAndDownloadsRespectRootsAndBookTypes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "shelf_100%")
	outside := filepath.Join(base, "shelf_100%-extra")
	s := openTestStore(t, filepath.Join(base, "index.db"))
	first := testFile(t, root, "reference.pdf", "%PDF-1.4\n")
	invalid := testFile(t, root, "other.pdf", "plain text")
	testFile(t, root, "text.txt", "text")
	testFile(t, root, "nested/book.fb2", fixtureFB2)
	out := testFile(t, outside, "untracked.pdf", "%PDF-1.4\n")
	if err := s.RecordDownload(first); err != nil {
		t.Fatal(err)
	}
	testList(t, s, "", 0)
	testScan(t, s, root, 2)
	b := testList(t, s, "pdf", 1)[0]
	if b.Title != "reference" || b.Format != "pdf" {
		t.Fatal(b)
	}
	if _, err := s.Add(invalid); !errors.Is(err, ebookmeta.ErrUnsupportedFormat) {
		t.Fatalf("invalid file: %v", err)
	}
	for _, name := range []string{out, invalid, testFile(t, root, "new/saved.pdf", "%PDF-1.5\n")} {
		if err := s.RecordDownload(name); err != nil {
			t.Fatal(err)
		}
	}
	testList(t, s, "", 3)
	testScan(t, s, root, 3)
	if err := s.Relocate(out, out); err != nil {
		t.Fatal(err)
	}
	testList(t, s, "untracked", 0)
	if _, err := s.Add(out); err != nil {
		t.Fatal(err)
	}
	testScan(t, s, root, 3)
	if testList(t, s, "untracked", 1)[0].Missing {
		t.Fatal("scanning a sibling marked this entry missing")
	}
	if err := s.SetTags(-1, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("SetTags missing id = %v", err)
	}
	if err := s.Remove(-1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Remove missing id = %v", err)
	}
}

func TestScanSkipsFileAndDirectoryLinks(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	pdf := testFile(t, outside, "remote.pdf", "%PDF-1.4\n")
	link := filepath.Join(dir, "alias.pdf")
	if err := os.Symlink(pdf, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked-dir")); err != nil {
		t.Fatal(err)
	}
	s := openTestStore(t, filepath.Join(t.TempDir(), "index.db"))
	testScan(t, s, dir, 0)
	if _, err := s.Add(link); !errors.Is(err, ebookmeta.ErrUnsupportedFormat) {
		t.Fatalf("Add(link) = %v", err)
	}
	if _, err := s.Scan(filepath.Join(dir, "linked-dir")); err == nil {
		t.Fatal("registered a symlink as a root")
	}
}

func TestFailedScanLeavesExistingEntriesAvailable(t *testing.T) {
	root := t.TempDir()
	first := testFile(t, root, "first.pdf", "%PDF-1.4\n")
	s := openTestStore(t, filepath.Join(t.TempDir(), "index.db"))
	testScan(t, s, root, 1)
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	testFile(t, root, "second.pdf", "%PDF-1.5\n")
	if _, err := s.db.Exec(`CREATE TRIGGER interrupt_scan BEFORE INSERT ON books
		BEGIN SELECT RAISE(ABORT, 'interrupted scan'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scan(root); err == nil {
		t.Fatal("expected the scan to fail")
	}
	if previous := testList(t, s, "", 1)[0]; previous.Missing || previous.Title != "first" {
		t.Fatalf("failed scan changed the library: %+v", previous)
	}
}

func TestConcurrentRegistrationKeepsOneEntry(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "index.db")
	first, second := openTestStore(t, dbPath), openTestStore(t, dbPath)
	path := testFile(t, t.TempDir(), "book.pdf", "%PDF-1.4\n")
	done := make(chan error, 2)
	for _, store := range []*Store{first, second} {
		go func() {
			_, err := store.Add(path)
			done <- err
		}()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	b := testList(t, first, "", 1)[0]
	if err := first.SetStatus(b.ID, "read"); err != nil {
		t.Fatal(err)
	}
	if err := second.Relocate(path, path); err != nil {
		t.Fatal(err)
	}
	if b2 := testList(t, second, "", 1)[0]; b2.ID != b.ID || b2.Status != "read" {
		t.Fatalf("refresh lost state: %+v", b2)
	}
}

func TestRecentOpensPersistAndOlderBookshelvesUpgrade(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "index.db")
	dir := t.TempDir()
	testFile(t, dir, "alpha.pdf", "%PDF-1.4\n")
	topic := testFile(t, dir, "topic.pdf", "%PDF-1.4\n")
	volume := testFile(t, dir, "volume.pdf", "%PDF-1.4\n")
	legacy := openTestStore(t, dbPath)
	testScan(t, legacy, dir, 3)
	id := testList(t, legacy, "volume", 1)[0].ID
	if err := legacy.SetStatus(id, "reading"); err != nil {
		t.Fatal(err)
	}
	// Reproduce a shelf saved before the last-opened field existed.
	if _, err := legacy.db.Exec("ALTER TABLE books DROP COLUMN last_opened"); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	s := openTestStore(t, dbPath)
	books := testList(t, s, "", 3)
	if books[0].Title != "alpha" || books[2].ID != id || books[2].LastOpened != "" || books[2].Status != "reading" {
		t.Fatalf("migration changed books: %+v", books)
	}
	if err := s.MarkOpened(testFile(t, t.TempDir(), "outside.pdf", "%PDF-1.4\n")); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkOpened(volume); err != nil {
		t.Fatal(err)
	}
	books = testList(t, s, "", 3)
	if books[0].ID != id || books[1].Title != "alpha" || books[2].LastOpened != "" {
		t.Fatalf("recently opened book is out of order: %+v", books)
	}
	stamp, err := time.Parse(time.RFC3339Nano, books[0].LastOpened)
	if err != nil || stamp.Location() != time.UTC {
		t.Fatalf("last opened = %q, %v", books[0].LastOpened, err)
	}
	if _, err := s.db.Exec("UPDATE books SET last_opened = '2000-01-01T00:00:00.000000000Z' WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkOpened(topic); err != nil {
		t.Fatal(err)
	}
	lastOpened := testList(t, s, "topic", 1)[0].LastOpened
	testScan(t, s, dir, 3)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, dbPath)
	books = testList(t, s, "", 3)
	if books[0].Title != "topic" || books[0].LastOpened != lastOpened || books[1].ID != id || books[2].Title != "alpha" {
		t.Fatalf("rescan or restart changed recent opens: %+v", books)
	}
}
