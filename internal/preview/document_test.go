package preview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakePDF counts actual tool invocations while avoiding platform dependencies.
type fakePDF struct {
	mu       sync.Mutex
	calls    map[string]int
	failNext map[string]bool
	image    []byte
}

func newFakePDF() *fakePDF {
	return &fakePDF{calls: map[string]int{}, failNext: map[string]bool{}, image: []byte("P6\n1 1\n255\n\x10\x20\x30")}
}

func (f *fakePDF) run(_ context.Context, _ int, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[name]++
	if f.failNext[name] {
		delete(f.failNext, name)
		return nil, errors.New(name + " unavailable")
	}
	switch name {
	case "pdfinfo":
		return []byte("Pages: 5\n"), nil
	case "pdftoppm":
		return f.image, nil
	case "pdftotext":
		return []byte("text " + args[1]), nil
	}
	panic(name)
}

func (f *fakePDF) check(t *testing.T, info, text, image int) {
	t.Helper()
	if f.calls["pdfinfo"] != info || f.calls["pdftotext"] != text || f.calls["pdftoppm"] != image {
		t.Fatalf("tool calls = %v, want info=%d text=%d image=%d", f.calls, info, text, image)
	}
}

func TestPDFDocumentSharesModesAndEvictsLeastRecentlyUsedPage(t *testing.T) {
	doc := NewDocument(twoPagePDF(t))
	fake := newFakePDF()
	doc.run = fake.run
	load := func(index int, graphics bool) Page {
		t.Helper()
		got, err := doc.Load(t.Context(), index, graphics)
		if err != nil || got.Index != index || (got.Image != nil) != graphics {
			t.Fatalf("page = %+v err = %v", got, err)
		}
		return got
	}
	first := load(0, false)
	fake.check(t, 1, 1, 0)
	if got := load(0, true); got.Text != first.Text {
		t.Fatalf("graphics upgrade lost text: %q", got.Text)
	}
	load(0, false)
	load(1, true)
	load(0, true)
	fake.check(t, 1, 2, 2)
	load(2, true)
	load(3, true)
	load(0, true)
	fake.check(t, 1, 4, 4)
	load(1, true)
	fake.check(t, 1, 5, 5)
	if len(doc.pdf.pages) > recentPDFPages {
		t.Fatal("recent page budget exceeded")
	}
}

func TestPDFDocumentRetriesOnlyTheFailedFormat(t *testing.T) {
	doc := NewDocument(twoPagePDF(t))
	fake := newFakePDF()
	doc.run = fake.run
	fake.failNext["pdftoppm"] = true
	got, err := doc.Load(t.Context(), 0, true)
	if err != nil || got.Image != nil || got.Text != "text 1" {
		t.Fatalf("fallback = %+v, %v", got, err)
	}
	got, err = doc.Load(t.Context(), 0, true)
	if err != nil || got.Image == nil {
		t.Fatalf("recovery = %+v, %v", got, err)
	}
	fake.check(t, 1, 1, 2)
	fake.failNext["pdftotext"] = true
	got, err = doc.Load(t.Context(), 1, true)
	if err != nil || got.Image == nil || got.Text != "pdftotext unavailable" {
		t.Fatalf("image fallback = %+v, %v", got, err)
	}
	got, err = doc.Load(t.Context(), 1, true)
	if err != nil || got.Text != "text 2" {
		t.Fatalf("text recovery = %+v, %v", got, err)
	}
	fake.check(t, 1, 3, 3)
}

func TestPDFDocumentInvalidatesAfterEditAndReplacement(t *testing.T) {
	path := twoPagePDF(t)
	doc := NewDocument(path)
	fake := newFakePDF()
	doc.run = fake.run
	load := func() {
		t.Helper()
		if _, err := doc.Load(t.Context(), 0, true); err != nil {
			t.Fatal(err)
		}
	}
	load()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString("\n")
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	load()
	fake.check(t, 2, 2, 2)
	// Same size and timestamps, different underlying file at the same path.
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(filepath.Dir(path), "replacement.pdf")
	if err := os.WriteFile(replacement, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, stat.ModTime(), stat.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	load()
	fake.check(t, 3, 3, 3)
}

func TestPDFDocumentCancelledWaiterStartsNoTools(t *testing.T) {
	doc := NewDocument(twoPagePDF(t))
	started, release := make(chan struct{}), make(chan struct{})
	var count atomic.Int32
	doc.run = func(ctx context.Context, _ int, _ string, _ ...string) ([]byte, error) {
		count.Add(1)
		close(started)
		<-release
		return nil, ctx.Err()
	}
	first, cancelFirst := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := doc.Load(first, 0, true)
		done <- err
	}()
	<-started
	cancelFirst()
	second, cancelSecond := context.WithCancel(t.Context())
	cancelSecond()
	_, err := doc.Load(second, 1, true)
	close(release)
	if !errors.Is(err, context.Canceled) || !errors.Is(<-done, context.Canceled) || count.Load() != 1 {
		t.Fatalf("queued cancellation = %v, starts = %d", err, count.Load())
	}
}

func TestPDFDocumentDropsFileChangedWhileRendering(t *testing.T) {
	path := twoPagePDF(t)
	doc := NewDocument(path)
	fake := newFakePDF()
	doc.run = func(ctx context.Context, limit int, name string, args ...string) ([]byte, error) {
		if name == "pdftotext" {
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				return nil, err
			}
			_, err = f.WriteString("\n")
			f.Close()
			if err != nil {
				return nil, err
			}
		}
		return fake.run(ctx, limit, name, args...)
	}
	if _, err := doc.Load(t.Context(), 0, true); err == nil || !strings.Contains(err.Error(), "PDF changed") {
		t.Fatalf("file revision changed, got %v", err)
	}
	doc.run = fake.run
	if _, err := doc.Load(t.Context(), 0, true); err != nil {
		t.Fatal(err)
	}
	fake.check(t, 2, 2, 2)
}
