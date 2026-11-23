package preview

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type unsupportedBBox struct{}

func (unsupportedBBox) Error() string { return "unsupported option" }
func (unsupportedBBox) ExitCode() int { return 99 }

func TestWordLayerFindsPopplerBehindXpdfAndRetainsTheWorkingTool(t *testing.T) {
	for _, available := range []bool{true, false} {
		label := "no Poppler"
		if available {
			label = "Poppler later on PATH"
		}
		t.Run(label, func(t *testing.T) {
			bookDir := t.TempDir()
			t.Chdir(bookDir)
			name := "pdftotext"
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			install := func(dir string) string {
				t.Helper()
				file := filepath.Join(dir, name)
				if err := os.WriteFile(file, []byte("fixture"), 0700); err != nil {
					t.Fatal(err)
				}
				return file
			}
			install(bookDir)
			old := install(t.TempDir())
			t.Setenv("NoDefaultCurrentDirectoryInExePath", "1")
			dirs := []string{filepath.Dir(old), "", ".", bookDir, filepath.Dir(old)}
			var capable string
			if available {
				capable = install(t.TempDir())
				dirs = append(dirs, filepath.Dir(capable))
			}
			t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
			var attempted []string
			fake := newFakePDF()
			doc := NewDocument(twoPagePDF(t))
			doc.run = func(ctx context.Context, limit int, tool string, args ...string) ([]byte, error) {
				if !slices.Contains(args, "-bbox-layout") {
					return fake.run(ctx, limit, tool, args...)
				}
				// Resolve a bare name exactly as the production runner does.
				file, err := exec.LookPath(tool)
				if err != nil {
					return nil, err
				}
				attempted = append(attempted, file)
				if file == old {
					return nil, unsupportedBBox{}
				}
				if file != capable {
					t.Fatalf("unexpected executable: %v", file)
				}
				return fake.run(ctx, limit, "pdftotext", args...)
			}
			for i := range 2 {
				p, err := doc.Load(t.Context(), i, true)
				if err != nil || p.Image == nil {
					t.Fatalf("missing PDF raster: %v", err)
				}
				if available && (len(p.Words) != 2 || p.WordsError != "") {
					t.Fatalf("working Poppler did not provide words: %+v, %q", p.Words, p.WordsError)
				}
				if !available && (len(p.Words) != 0 || !strings.Contains(p.WordsError, "Poppler's pdftotext with -bbox-layout")) {
					t.Fatalf("missing action for incompatible Xpdf: %q", p.WordsError)
				}
			}
			want := []string{old, old}
			if available {
				want = []string{old, capable, capable}
			}
			if !slices.Equal(want, attempted) {
				t.Fatalf("executables attempted: %v, want %v", attempted, want)
			}
		})
	}
}
