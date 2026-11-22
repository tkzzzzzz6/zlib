package ebookmeta

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestPreviewFollowsSpineAndPreservesProse(t *testing.T) {
	opf := strings.Replace(opf2, "<manifest>", `<manifest>
		<item id="last" href="Text/part2.xhtml" media-type="application/xhtml+xml"/>
		<item id="first" href="Text/part1.xhtml" media-type="application/xhtml+xml"/>`, 1)
	opf = strings.Replace(opf, `<itemref idref="ch1"/>`, `<itemref idref="first"/><itemref idref="ch1" linear="no"/><itemref idref="last"/>`, 1)
	data := buildEPUB(t, opf, map[string][]byte{
		"OEBPS/Text/part1.xhtml": []byte(`<html><head><title>Introduction</title></head><body><p>Read <i>this</i> sentence.</p></body></html>`),
		"OEBPS/Text/part2.xhtml": []byte(`<html><head><style>hidden styling</style></head><body><h1>Next <em>steps</em></h1><p>A<i>B</i>C &amp; D.</p><script>hidden script</script><p>Second paragraph.</p></body></html>`),
	})
	titles, body, err := ReadEPUBChapter(t.Context(), bytes.NewReader(data), int64(len(data)), 1)
	if err != nil || !slices.Equal(titles, []string{"Introduction", "Next steps"}) || body != "Next steps\nABC & D.\nSecond paragraph." {
		t.Fatalf("titles = %q, body = %q, err = %v", titles, body, err)
	}
	_, body, err = ReadEPUBChapter(t.Context(), bytes.NewReader(data), int64(len(data)), 0)
	if err != nil || body != "Read this sentence." {
		t.Fatalf("first = %q, err = %v", body, err)
	}
}

func TestPreviewRejectsInvalidOrUnavailableChapters(t *testing.T) {
	for _, tc := range []struct {
		name  string
		href  string
		index int
		want  string
	}{
		{"relative", "../../outside.xhtml", 0, "leaves the archive"},
		{"remote", "https://example.com/outside.xhtml", 0, "outside the archive"},
		{"missing", "missing.xhtml", 0, "missing"},
		{"range", "Text/ch%201.xhtml", 1, "choose a chapter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := buildEPUB(t, strings.Replace(opf2, "Text/ch%201.xhtml", tc.href, 1), nil)
			_, _, err := ReadEPUBChapter(t.Context(), bytes.NewReader(data), int64(len(data)), tc.index)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	data := buildEPUB(t, opf2, map[string][]byte{"META-INF/encryption.xml": []byte(`<encryption><CipherReference URI="OEBPS/Text/ch%201.xhtml"/></encryption>`)})
	_, _, err := ReadEPUBChapter(t.Context(), bytes.NewReader(data), int64(len(data)), 0)
	if !errors.Is(err, ErrEncrypted) {
		t.Fatalf("encrypted = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	data = buildEPUB(t, opf2, nil)
	_, _, err = ReadEPUBChapter(ctx, bytes.NewReader(data), int64(len(data)), 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled = %v", err)
	}
}

func TestPreviewBoundsLargeChapterText(t *testing.T) {
	data := buildEPUB(t, strings.Replace(opf2, "ch%201.xhtml", "huge.xhtml", 1), map[string][]byte{
		"OEBPS/Text/huge.xhtml": []byte("<html><body><p>" + strings.Repeat("段", 750000) + "</p></body></html>"),
	})
	_, body, err := ReadEPUBChapter(t.Context(), bytes.NewReader(data), int64(len(data)), 0)
	if err != nil || !strings.HasPrefix(body, "段") || !strings.HasSuffix(body, chapterTruncated) || len(body) > (129<<10) || strings.Count(body, chapterTruncated) != 1 {
		t.Fatalf("large preview has %d bytes; err = %v", len(body), err)
	}
}
