package cli

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/sixel"
	"github.com/heartleo/zlib/internal/preview"
	zlib "github.com/heartleo/zlib/internal/zlib"
)

const coverGraphicsID = 5917697
const coverProbe = "\x1b_Ga=q,i=5917697,s=1,v=1,f=24,t=d;AAAA\x1b\\\x1b[c\x1b[16t\x1b[14t" + iTermCellSizeQuery
const coverDelete = "\x1b_Ga=d,d=I,i=5917697,q=2\x1b\\"
const syncBegin = "\x1b[?2026h"
const syncEnd = "\x1b[?2026l"
const coverWake = "\x1b7\x1b8"

var kittyPageSequence atomic.Uint32

type coverCapabilities struct {
	kitty, sixel            bool
	cellWidth, cellHeight   int
	pixelWidth, pixelHeight int
	cols, rows              int
	// scale converts reported sizes to physical pixels. iTerm2 on a HiDPI Mac
	// reports cell and window sizes in points but draws images in pixels; it
	// states the factor through OSC 1337 ReportCellSize. Zero means 1.
	scale float64
}

// iTermCellSizeQuery asks iTerm2 for its pixel scale. Other terminals ignore it.
const iTermCellSizeQuery = "\x1b]1337;ReportCellSize\a"

// parseITermScale reads the scale from the reply
// "ESC ] 1337;ReportCellSize=<h>;<w>;<scale> ST". The framing may or may not be
// present, depending on how the input parser hands the sequence over.
func parseITermScale(osc string) (float64, bool) {
	osc = strings.TrimPrefix(osc, "\x1b]")
	osc = strings.TrimSuffix(strings.TrimSuffix(osc, "\x1b\\"), "\a")
	rest, ok := strings.CutPrefix(osc, "1337;ReportCellSize=")
	if !ok {
		return 0, false
	}
	parts := strings.Split(rest, ";")
	if len(parts) < 3 {
		return 0, false
	}
	scale, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if err != nil || scale < 1 || scale > 8 {
		return 0, false
	}
	return scale, true
}

func (c coverCapabilities) cellSize() (int, int) {
	w, h := 0, 0
	if c.cellWidth > 0 && c.cellHeight > 0 {
		w, h = c.cellWidth, c.cellHeight
	} else if c.cols > 0 && c.rows > 0 {
		w, h = c.pixelWidth/c.cols, c.pixelHeight/c.rows
	}
	if c.scale > 1 {
		w, h = int(float64(w)*c.scale+0.5), int(float64(h)*c.scale+0.5)
	}
	return w, h
}

func (c coverCapabilities) protocol() string {
	w, h := c.cellSize()
	if w <= 0 || h <= 0 || w > 512 || h > 512 {
		return ""
	}
	if c.kitty {
		return "Kitty"
	}
	if c.sixel {
		return "Sixel"
	}
	return ""
}

type coverSource struct {
	url                        string
	img                        image.Image
	width, height              int
	area                       image.Rectangle
	documentPage               bool // the whole preview body, including in narrow terminals
	zoomPercent                int  // relative to a fitted PDF page; zero is the initial fit
	panX, panY                 int  // -100 and 100 select opposite edges of a zoomed page
	words                      []preview.Word
	selectedFirst, selectedEnd int // half-open range in reading order
	selecting                  bool
	selectionAnchor            int
}

func (s coverSource) bounds() image.Rectangle {
	if !s.area.Empty() {
		return s.area
	}
	return image.Rect(2, 1, s.width-2, s.height-2)
}

type coverFrame struct {
	data, protocol          string
	pageKey                 string // page identity shared by its zoom and pan frames
	x, y, cols, rows        int
	pixelWidth, pixelHeight int
	cellWidth, cellHeight   int
	id                      int               // the Kitty image id
	wordRects               []image.Rectangle // viewport pixels in reading order
	// more are further images drawn with this one, such as the second cover
	// of a comparison. They are erased and redrawn together.
	more []*coverFrame
}

// all is the frame and the ones drawn with it.
func (f *coverFrame) all() []*coverFrame {
	if f == nil {
		return nil
	}
	return append([]*coverFrame{f}, f.more...)
}

type coverEncodedMsg struct {
	generation uint64
	frame      *coverFrame
}

type coverRenderKey struct {
	layout                     string // sources, placement, protocol and cell size
	zoomPercent, panX, panY    int    // viewport within the same PDF page
	selectedFirst, selectedEnd int
}

// Keep a few completed views of the current PDF page for wheel reversals and
// fit resets. The four slots together retain at most 8 MiB of encoded data.
type pageViewportCache [4]struct {
	key   coverRenderKey
	frame *coverFrame
}

func (c *pageViewportCache) remember(key coverRenderKey, frame *coverFrame) {
	// A drag can produce many intermediate selections. Keep the clean
	// viewport handy so clearing the highlight needs no new encoding.
	if frame == nil || frame.pageKey == "" || key.selectedEnd > key.selectedFirst {
		return
	}
	if len(frame.data)+len(frame.wordRects)*4*(strconv.IntSize/8) > 2<<20 {
		return
	}
	copy(c[1:], c[:len(c)-1])
	c[0].key, c[0].frame = key, frame
}

func (c *pageViewportCache) lookup(key coverRenderKey) *coverFrame {
	for _, entry := range c {
		if entry.key == key {
			return entry.frame
		}
	}
	return nil
}

// The outer model owns terminal capabilities across shell page generations.
// Network requests and keyboard navigation remain in the existing shared pages.
type coverGraphicsModel struct {
	inner             tea.Model
	output            *coverOutput
	caps              coverCapabilities
	generation        uint64
	key               coverRenderKey
	frame             *coverFrame
	viewports         pageViewportCache
	encoding          bool
	encodingKey       coverRenderKey
	encodingAnchor    int
	supported         bool
	encodedGeneration uint64
}

func runCoverProgram(model tea.Model) (tea.Model, error) {
	coverGraphicsAvailable.Store(false) // until the terminal proves otherwise
	out := &coverOutput{File: os.Stdout, target: os.Stdout}
	defer out.cleanup()
	defer coverGraphicsAvailable.Store(false)
	m := &coverGraphicsModel{inner: model, output: out}
	result, err := tea.NewProgram(m, tea.WithOutput(out)).Run()
	if wrapped, ok := result.(*coverGraphicsModel); ok {
		return wrapped.inner, err
	}
	return model, err
}

func (m *coverGraphicsModel) Init() tea.Cmd { return tea.Batch(m.inner.Init(), tea.Raw(coverProbe)) }

// coverSlotter is a page that shows covers of its own, such as the metadata
// page: it says which images go where, or none while something covers them.
type coverSlotter interface {
	coverSlots() []coverSource
}

// sources are the covers to draw now: the book card's one, or those a page
// such as the metadata page lays out itself.
func (m *coverGraphicsModel) sources() []coverSource {
	model := m.inner
	if shell, ok := model.(shellModel); ok {
		if shell.typing {
			return nil
		}
		if page, ok := shell.page.(coverSlotter); ok {
			return page.coverSlots()
		}
		if shell.kind != "list" {
			return nil
		}
		model = shell.page
	}
	if page, ok := model.(coverSlotter); ok {
		return page.coverSlots()
	}
	if s := m.cardSource(model); s.img != nil {
		return []coverSource{s}
	}
	return nil
}

func (m *coverGraphicsModel) source() coverSource {
	if s := m.sources(); len(s) > 0 {
		return s[0]
	}
	return coverSource{}
}

func (m *coverGraphicsModel) cardSource(model tea.Model) coverSource {
	switch page := model.(type) {
	case searchSelectModel:
		if !page.quitting && page.state == searchStateResults && len(page.books) > 0 && !page.confirmDownload && (page.detailOpen || page.hasBookCard()) {
			return cardCoverSource(page.details, page.selectedBook().Cover, page.width, page.height)
		}
	case historySelectModel:
		if !page.quitting && page.state == historyStateResults && !page.confirmDownload {
			book := page.details.book(page.selectedBookRef(), zlib.Book{})
			return cardCoverSource(page.details, book.Cover, page.width, page.height)
		}
	}
	return coverSource{}
}

// cardCoverSource is the loaded cover for the book card, or no source while
// the cover is still missing.
func cardCoverSource(d bookDetailState, url string, width, height int) coverSource {
	if d.covers[url] == "" {
		return coverSource{}
	}
	return coverSource{url: url, img: d.coverImages[url], width: width, height: height, area: bookCardCoverBounds()}
}

func (m *coverGraphicsModel) sourceKey() coverRenderKey {
	srcs := m.sources()
	if len(srcs) == 0 || m.caps.protocol() == "" {
		return coverRenderKey{}
	}
	w, h := m.caps.cellSize()
	var key coverRenderKey
	var layout strings.Builder
	for _, s := range srcs {
		if s.img == nil || (!s.documentPage && !coverSizeFits(s.width, s.height)) {
			return coverRenderKey{}
		}
		fmt.Fprintf(&layout, "%s/%d/%d/%d/%d/%s/%v|", s.url, s.width, s.height, w, h, m.caps.protocol(), s.bounds())
		if s.documentPage {
			key.zoomPercent, key.panX, key.panY = s.zoomPercent, s.panX, s.panY
			key.selectedFirst, key.selectedEnd = s.selectedFirst, s.selectedEnd
		}
	}
	key.layout = layout.String()
	return key
}

func (m *coverGraphicsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd, query, wake tea.Cmd
	switch event := msg.(type) {
	case uv.KittyGraphicsEvent:
		if event.Options.ID == coverGraphicsID {
			m.caps.kitty = string(event.Payload) == "OK"
		}
	case uv.PrimaryDeviceAttributesEvent:
		m.caps.sixel = slices.Contains(event, 4)
	case uv.CellSizeEvent:
		m.caps.cellWidth, m.caps.cellHeight = event.Width, event.Height
	case uv.PixelSizeEvent:
		m.caps.pixelWidth, m.caps.pixelHeight = event.Width, event.Height
	case uv.UnknownOscEvent:
		if scale, ok := parseITermScale(string(event)); ok {
			m.caps.scale = scale
		}
	case coverEncodedMsg:
		m.encoding = false
		if event.generation == m.generation {
			m.encodedGeneration = event.generation
			m.frame = event.frame
			m.viewports.remember(m.key, event.frame)
			if event.frame == nil && m.source().documentPage {
				m.inner, cmd = m.inner.Update(previewImageFailedMsg{})
			}
			wake = tea.Raw(coverWake)
		} else if event.frame != nil && m.continuingDrag() {
			// A long drag can move faster than an encoder. Show completed
			// progress on this gesture while starting its newest range below.
			m.frame = event.frame
			wake = tea.Raw(coverWake)
		}
	default:
		if size, ok := msg.(tea.WindowSizeMsg); ok {
			m.caps.cols, m.caps.rows = size.Width, size.Height
			m.caps.cellWidth, m.caps.cellHeight = 0, 0
			m.caps.pixelWidth, m.caps.pixelHeight = 0, 0
			query = tea.Raw("\x1b[16t\x1b[14t" + iTermCellSizeQuery)
		}
		m.inner, cmd = m.inner.Update(m.imagePointer(msg))
	}
	// Tell the pages whether covers exist at all. They stay hidden until a
	// protocol and pixel metrics are confirmed; on a change, replay the window
	// size so the pages relayout and start their cover requests.
	if supported := m.caps.protocol() != ""; supported != m.supported {
		m.supported = supported
		coverGraphicsAvailable.Store(supported)
		if m.caps.cols > 0 && m.caps.rows > 0 {
			var resize tea.Cmd
			m.inner, resize = m.inner.Update(tea.WindowSizeMsg{Width: m.caps.cols, Height: m.caps.rows})
			cmd = tea.Batch(cmd, resize)
		}
		var refresh tea.Cmd
		m.inner, refresh = m.inner.Update(previewGraphicsMsg(supported))
		cmd = tea.Batch(cmd, refresh)
	}
	key := m.sourceKey()
	if key != m.key {
		m.generation++
		// During zoom or pan the old pixels still belong to this page. Keep
		// them on screen while the single encoder catches up with input.
		if key.layout != m.key.layout {
			m.frame = nil
			m.viewports = pageViewportCache{}
			wake = tea.Raw(coverWake)
		} else if cached := m.viewports.lookup(key); cached != nil {
			m.frame = cached
			m.encodedGeneration = m.generation
			wake = tea.Raw(coverWake)
		}
		m.key = key
	}
	if key.layout != "" && !m.encoding && m.encodedGeneration != m.generation {
		m.encoding = true
		srcs, c, generation := m.sources(), m.caps, m.generation
		m.encodingKey, m.encodingAnchor = key, srcs[0].selectionAnchor
		encode := func() tea.Msg { return coverEncodedMsg{generation, encodeCoverFrames(srcs, c)} }
		return m, tea.Batch(cmd, query, wake, encode)
	}
	return m, tea.Batch(cmd, query, wake)
}

func (m *coverGraphicsModel) continuingDrag() bool {
	pending := m.encodingKey
	if pending.selectedFirst >= pending.selectedEnd {
		return false
	}
	s := m.source()
	pending.selectedFirst, pending.selectedEnd = m.key.selectedFirst, m.key.selectedEnd
	return s.selecting && m.encodingAnchor == s.selectionAnchor && pending == m.key
}

func (m *coverGraphicsModel) View() tea.View {
	v := m.inner.View()
	frame := m.frame
	key := m.sourceKey()
	if key.layout == "" || m.key != key {
		frame = nil
	}
	srcs := m.sources()
	// A capable terminal should show the final pixel cover once, rather than
	// briefly drawing a block thumbnail while its native encoding is pending.
	waitingCard := len(srcs) > 0 && !srcs[0].area.Empty() && key.layout != "" && m.encodedGeneration != m.generation
	if frame != nil || waitingCard {
		// A book card shares its row with metadata. A PDF image replaces
		// the whole text body, including the margins outside its placement.
		lines := strings.Split(v.Content, "\n")
		for _, s := range srcs {
			area := s.bounds()
			if s.documentPage {
				area.Min.X, area.Max.X = 0, s.width
			}
			for i, line := range lines {
				if i >= area.Min.Y && i < area.Max.Y {
					lines[i] = ansi.Cut(line, 0, area.Min.X) + strings.Repeat(" ", area.Dx()) + ansi.Cut(line, area.Max.X, ansi.StringWidth(line))
				}
			}
		}
		v.Content = strings.Join(lines, "\n")
	}
	m.output.set(frame)
	return v
}

// encodeCoverFrames encodes every source, each with its own Kitty image id. It
// returns nil when any of them cannot be drawn, so a pair is never half shown.
func encodeCoverFrames(srcs []coverSource, c coverCapabilities) *coverFrame {
	var first *coverFrame
	for i, s := range srcs {
		f := encodeCoverFrameID(s, c, coverGraphicsID+i)
		if f == nil {
			return nil
		}
		if first == nil {
			first = f
		} else {
			first.more = append(first.more, f)
		}
	}
	return first
}

func encodeCoverFrame(s coverSource, c coverCapabilities) *coverFrame {
	return encodeCoverFrameID(s, c, coverGraphicsID)
}

func encodeCoverFrameID(s coverSource, c coverCapabilities, id int) *coverFrame {
	cw, ch := c.cellSize()
	if cw <= 0 || ch <= 0 || s.img == nil {
		return nil
	}
	b := s.img.Bounds()
	if b.Empty() {
		return nil
	}
	// Scale to fit the reserved cells, up or down, keeping the aspect ratio.
	// Terminals report cell size in physical pixels, so on HiDPI screens such as
	// macOS Retina a small source shown 1:1 would fill only a fraction of the slot.
	w, h := b.Dx(), b.Dy()
	area := s.bounds()
	maxW, maxH := area.Dx()*cw, area.Dy()*ch
	if s.documentPage {
		// A large terminal must not turn a bounded page raster into an
		// unbounded bitmap during Sixel or Kitty encoding.
		maxW, maxH = min(maxW, 2048), min(maxH, 2048)
	}
	if w*maxH >= h*maxW {
		h = max(1, h*maxW/w)
		w = maxW
	} else {
		w = max(1, w*maxH/h)
		h = maxH
	}
	if w <= 0 || h <= 0 {
		return nil
	}
	img := s.img
	var wordRects []image.Rectangle
	if s.documentPage {
		view := s.pageViewport(w, h, maxW, maxH)
		img, w, h = view.raster(img), view.size.X, view.size.Y
		wordRects = view.wordRectangles(s.words)
		img = view.highlight(img, wordRects, s.selectedFirst, s.selectedEnd)
	}
	if w != img.Bounds().Dx() || h != img.Bounds().Dy() {
		img = resizeCover(img, w, h)
	}
	cols, rows := (w+cw-1)/cw, (h+ch-1)/ch
	f := &coverFrame{protocol: c.protocol(), x: area.Min.X + (area.Dx()-cols)/2, y: area.Min.Y + (area.Dy()-rows)/2, cols: cols, rows: rows, id: id}
	f.pixelWidth, f.pixelHeight = w, h
	if s.documentPage {
		f.wordRects = wordRects
		f.pageKey = s.url
		f.cellWidth, f.cellHeight = cw, ch
		if f.protocol == "Kitty" {
			// Retransmitting a Kitty image ID discards its existing placements.
			// Keep this frame separate until the writer can retire the old one.
			f.id = int(0x40000000 | (kittyPageSequence.Add(1) & 0x3fffffff))
		}
	}
	var data bytes.Buffer
	if f.protocol == "Kitty" {
		compression := png.DefaultCompression
		if s.documentPage {
			img = compactGrayPage(img)
			compression = png.BestSpeed
		}
		if err := (&png.Encoder{CompressionLevel: compression}).Encode(&data, img); err != nil {
			return nil
		}
		payload := base64.StdEncoding.EncodeToString(data.Bytes())
		var out strings.Builder
		for offset := 0; offset < len(payload); offset += 4096 {
			end := min(offset+4096, len(payload))
			more := 0
			if end < len(payload) {
				more = 1
			}
			opts := fmt.Sprintf("m=%d", more)
			if offset == 0 {
				opts = fmt.Sprintf("a=T,f=100,t=d,i=%d,q=2,C=1,m=%d", f.id, more)
			}
			out.WriteString(ansi.KittyGraphics([]byte(payload[offset:end]), opts))
		}
		f.data = out.String()
	} else if f.protocol == "Sixel" {
		if !s.documentPage || !encodePageSixel(&data, img) {
			if err := (&sixel.Encoder{}).Encode(&data, img); err != nil {
				return nil
			}
		}
		f.data = ansi.SixelGraphics(0, 1, 0, data.Bytes())
	} else {
		return nil
	}
	return f
}

// Graphics bypass the cell renderer but share its output writer. Each picture
// follows the text frame in the same write; no independent stdout writes race
// the renderer.
type coverOutput struct {
	*os.File
	target      io.Writer
	mu          sync.Mutex
	want, shown *coverFrame
	// A first Sixel written by a raw wake may precede the renderer's erase
	// of the text fallback. Repaint once when that cell update arrives.
	awaitingText bool
}

func (w *coverOutput) set(f *coverFrame) { w.mu.Lock(); w.want = f; w.mu.Unlock() }

func eraseCover(f *coverFrame) string {
	var out strings.Builder
	for _, p := range f.all() {
		if p.protocol == "Kitty" {
			if p.id == 0 || p.id == coverGraphicsID {
				out.WriteString(coverDelete)
			} else {
				fmt.Fprintf(&out, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", p.id)
			}
			continue
		}
		out.WriteString("\x1b7")
		for y := range p.rows {
			fmt.Fprintf(&out, "\x1b[%d;%dH%s", p.y+y+1, p.x+1, strings.Repeat(" ", p.cols))
		}
		out.WriteString("\x1b8")
	}
	return out.String()
}

// Sixel overwrites pixels in place. Clear just the old cells exposed by a
// replacement, including a trailing cell when fewer of its pixels are covered.
func eraseSixelMargins(old, next *coverFrame) string {
	occupied := image.Rect(old.x, old.y, old.x+old.cols, old.y+old.rows)
	covered := image.Rect(next.x, next.y, next.x+next.cols, next.y+next.rows)
	cw, ch := next.cellWidth, next.cellHeight
	if next.pixelWidth%cw != 0 && next.x*cw+next.pixelWidth < old.x*cw+old.pixelWidth {
		covered.Max.X--
	}
	if next.pixelHeight%ch != 0 && next.y*ch+next.pixelHeight < old.y*ch+old.pixelHeight {
		covered.Max.Y--
	}
	overlap := occupied.Intersect(covered)
	if overlap == occupied {
		return ""
	}
	var out strings.Builder
	out.WriteString("\x1b7")
	clearSpan := func(y, left, right int) {
		if right > left {
			fmt.Fprintf(&out, "\x1b[%d;%dH%s", y+1, left+1, strings.Repeat(" ", right-left))
		}
	}
	for y := occupied.Min.Y; y < occupied.Max.Y; y++ {
		if y < overlap.Min.Y || y >= overlap.Max.Y {
			clearSpan(y, occupied.Min.X, occupied.Max.X)
		} else {
			clearSpan(y, occupied.Min.X, overlap.Min.X)
			clearSpan(y, overlap.Max.X, occupied.Max.X)
		}
	}
	out.WriteString("\x1b8")
	return out.String()
}

func (f *coverFrame) survivesText(p []byte) bool {
	if bytes.Contains(p, []byte("\x1b[2J")) || bytes.Contains(p, []byte("\x1b[3J")) {
		return false
	}
	if f.pageKey == "" {
		// Kitty retains card placements. Sixel cards share rows with text
		// and must be repainted after the cell renderer writes to them.
		return f.protocol == "Kitty"
	}
	// The PDF body is reserved, so header and footer edits leave it alone.
	// The renderer may erase to the end of the screen on a full repaint.
	return !bytes.Contains(p, []byte("\x1b[J")) && !bytes.Contains(p, []byte("\x1b[0J")) && !bytes.Contains(p, []byte("\x1b[1J"))
}

func (w *coverOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.want == nil && w.shown == nil {
		return w.target.Write(p)
	}
	want := w.want
	if bytes.Contains(p, []byte("\x1b[?1049l")) {
		want = nil
		w.want = nil
	}
	var out bytes.Buffer
	out.WriteString(syncBegin)
	var eraseAfter string
	old := w.shown
	samePage := old != nil && want != nil && old.pageKey != "" && old.pageKey == want.pageKey && old.protocol == want.protocol
	if w.shown != want {
		switch {
		case samePage && want.protocol == "Kitty":
			eraseAfter = eraseCover(old)
		case samePage && want.protocol == "Sixel" && old.cellWidth == want.cellWidth && old.cellHeight == want.cellHeight:
			out.WriteString(eraseSixelMargins(old, want))
		default:
			out.WriteString(eraseCover(old))
		}
	}
	// Append graphics before an existing synchronized-update terminator.
	end := bytes.HasSuffix(p, []byte(syncEnd))
	if end {
		p = p[:len(p)-len(syncEnd)]
	}
	out.Write(p)
	wake := bytes.Contains(p, []byte(coverWake))
	retained := want != nil && want == w.shown && (!w.awaitingText || wake) && want.survivesText(p)
	if want != nil && !retained {
		for _, p := range want.all() {
			fmt.Fprintf(&out, "\x1b7\x1b[%d;%dH%s\x1b8", p.y+1, p.x+1, p.data)
		}
	}
	out.WriteString(eraseAfter)
	out.WriteString(syncEnd)
	n, err := w.target.Write(out.Bytes())
	if err != nil {
		return 0, err
	}
	if n != out.Len() {
		return 0, io.ErrShortWrite
	}
	w.shown = want
	w.awaitingText = wake && want != nil && want.pageKey != "" && want.protocol == "Sixel" && (w.awaitingText || !samePage)
	length := len(p)
	if end {
		length += len(syncEnd)
	}
	return length, nil
}

func (w *coverOutput) cleanup() {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = io.WriteString(w.target, eraseCover(w.shown))
	w.shown = nil
	w.want = nil
	w.awaitingText = false
}
