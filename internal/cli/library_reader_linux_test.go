package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDesktopReaderCanOutliveTheBookshelfRequest(t *testing.T) {
	dir := t.TempDir()
	// A desktop launcher is allowed to wait for the reading session to end.
	launcher := "#!/bin/sh\nwhile [ -f \"$1\" ]; do sleep 0.1; done\n"
	if err := os.WriteFile(filepath.Join(dir, "xdg-open"), []byte(launcher), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	path := filepath.Join(dir, "sample book.pdf")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- launchBookReader(path) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Error("launch blocked on the reading session")
	}
}
