// SPDX-License-Identifier: MIT

package console

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// terminalScreen is a test-only decoder for the xterm output used by the PTY
// scenarios. It models cells rather than filtering control bytes: BS moves the
// cursor, wide glyphs occupy two cells, and overwritten/erased text disappears.
// Unsupported controls fail closed. Grid, wire, pending sequence and cell text
// bounds are independent of the child timeout. Control semantics are documented
// at https://invisible-island.net/xterm/ctlseqs/ctlseqs.html.
type terminalScreen struct {
	w, h           int
	normal, alt    terminalBuffer
	active         *terminalBuffer
	revision       uint64
	autoWrap, sync bool
	charset        [2]byte
	gl             int
	savedCharset   [2]byte
	savedGL        int
	pending        []byte
	total          int
	dirty          []bool
	observe        func(string)
}

type terminalBuffer struct {
	cells []terminalCell
	x, y  int
	wrap  bool
}

type terminalCell struct {
	text         string
	width        int
	continuation bool
	written      uint64
}

func newTerminalScreenObserver(w, h int, observe func(string)) *terminalScreen {
	if w < 2 || w > 160 || h < 1 || h > 60 {
		panic("PTY observer grid outside test bound")
	}
	s := &terminalScreen{w: w, h: h, autoWrap: true, charset: [2]byte{'B', 'B'}, dirty: make([]bool, h), observe: observe}
	s.normal.cells, s.alt.cells = make([]terminalCell, w*h), make([]terminalCell, w*h)
	s.active = &s.normal
	return s
}

func (s *terminalScreen) feed(data []byte) error {
	s.total += len(data)
	if s.total > 256<<10 {
		return errors.New("PTY observer wire exceeds test bound")
	}
	data = append(s.pending, data...)
	s.pending = nil
	for len(data) > 0 {
		n, err := s.step(data)
		if err != nil {
			return err
		}
		if n == 0 {
			if len(data) > 4096 {
				return errors.New("PTY observer incomplete sequence exceeds test bound")
			}
			s.pending = append([]byte(nil), data...)
			return nil
		}
		data = data[n:]
		s.flushRows()
	}
	return nil
}

func (s *terminalScreen) finish() error {
	if len(s.pending) != 0 || s.sync {
		return errors.New("PTY observer ended within a sequence or synchronized redraw")
	}
	return nil
}

func (s *terminalScreen) step(data []byte) (int, error) {
	b := s.active
	switch data[0] {
	case '\x1b':
		if len(data) < 2 {
			return 0, nil
		}
		switch data[1] {
		case '[':
			for i := 2; i < len(data); i++ {
				if i > 4096 {
					return 0, errors.New("PTY observer CSI exceeds test bound")
				}
				if data[i] >= 0x40 && data[i] <= 0x7e {
					return i + 1, s.csi(string(data[2:i]), data[i])
				}
				if data[i] < 0x20 || data[i] > 0x3f {
					return 0, errors.New("PTY observer invalid CSI byte")
				}
			}
			return 0, nil
		case ']':
			for i := 2; i < len(data); i++ {
				if i > 4096 {
					return 0, errors.New("PTY observer OSC exceeds test bound")
				}
				if data[i] == '\a' {
					return i + 1, terminalOSC(string(data[2:i]))
				}
				if data[i] == '\x1b' {
					if i+1 == len(data) {
						return 0, nil
					}
					if data[i+1] != '\\' {
						return 0, errors.New("PTY observer invalid OSC terminator")
					}
					return i + 2, terminalOSC(string(data[2:i]))
				}
			}
			return 0, nil
		case '(', ')':
			if len(data) < 3 {
				return 0, nil
			}
			if data[2] != 'B' && data[2] != '0' {
				return 0, errors.New("PTY observer unsupported character set")
			}
			s.charset[int(data[1]-'(')] = data[2]
			return 3, nil
		case '=', '>': // Application/numeric keypad changes input, not cells.
			return 2, nil
		case 'D', 'E':
			if data[1] == 'E' {
				b.x = 0
			}
			s.lineFeed()
			return 2, nil
		default:
			return 0, fmt.Errorf("PTY observer unsupported ESC %q", data[1])
		}
	case '\b':
		b.x, b.wrap = max(0, b.x-1), false
	case '\r':
		b.x, b.wrap = 0, false
	case '\n', '\v', '\f':
		s.lineFeed()
	case '\t':
		b.x, b.wrap = min(s.w-1, (b.x/8+1)*8), false
	case '\x0e':
		s.gl = 1
	case '\x0f':
		s.gl = 0
	case '\x00', '\a', '\x7f': // No cell or cursor change in xterm.
	default:
		if data[0] < 0x20 {
			return 0, fmt.Errorf("PTY observer unsupported control %x", data[0])
		}
		if !utf8.FullRune(data) {
			return 0, nil
		}
		r, n := utf8.DecodeRune(data)
		if r == utf8.RuneError && n == 1 || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return 0, errors.New("PTY observer invalid or unsupported text rune")
		}
		if s.charset[s.gl] == '0' && r >= 0x60 && r <= 0x7e {
			r = []rune("◆▒␉␌␍␊°±␤␋┘┐┌└┼⎺⎻─⎼⎽├┤┴┬│≤≥π≠£·")[r-0x60]
		}
		return n, s.put(r)
	}
	return 1, nil
}

func terminalOSC(text string) error {
	command, _, _ := strings.Cut(text, ";")
	switch command {
	case "0", "1", "2", "8", "12", "112": // Title, hyperlink or cursor colour.
		return nil
	default:
		return fmt.Errorf("PTY observer unsupported OSC command %q", command)
	}
}

func terminalParams(body string) ([]int, error) {
	parts := strings.Split(body, ";")
	if len(parts) > 16 {
		return nil, errors.New("PTY observer too many CSI parameters")
	}
	values := make([]int, len(parts))
	for i, part := range parts {
		if part == "" {
			continue
		}
		if len(part) > 4 || strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return nil, errors.New("PTY observer invalid CSI parameter")
		}
		values[i], _ = strconv.Atoi(part)
	}
	return values, nil
}

func (s *terminalScreen) csi(body string, final byte) error {
	// These precise keyboard protocol controls cannot change displayed cells.
	if final == 'm' && (body == ">4;2" || body == ">4;0") || final == 'u' && (body == ">1" || body == "<") {
		return nil
	}
	if final == 'q' && strings.HasSuffix(body, " ") {
		values, err := terminalParams(strings.TrimSuffix(body, " "))
		if err != nil || len(values) != 1 || values[0] > 6 {
			return errors.New("PTY observer unsupported cursor style")
		}
		return nil
	}
	private := strings.HasPrefix(body, "?")
	if private {
		body = strings.TrimPrefix(body, "?")
	}
	values, err := terminalParams(body)
	if err != nil {
		return err
	}
	if private {
		if final != 'h' && final != 'l' {
			return errors.New("PTY observer unsupported private CSI")
		}
		for _, value := range values {
			switch value {
			case 1, 12, 25, 1000, 1002, 1003, 1004, 1006, 2004, 9001:
				// Input reporting or cursor visibility; the cell grid is unchanged.
			case 7:
				s.autoWrap = final == 'h'
				s.active.wrap = false
			case 2026:
				// Do not observe incomplete intermediate synchronized redraws.
				s.sync = final == 'h'
			case 1049:
				if final == 'h' {
					if s.active != &s.normal {
						return errors.New("PTY observer repeated alternate-screen entry")
					}
					s.savedCharset, s.savedGL = s.charset, s.gl
					// Xterm clears the alternate grid without homing the saved
					// cursor. Clearing resets its pending-wrap flag only.
					s.alt = terminalBuffer{cells: make([]terminalCell, s.w*s.h), x: s.normal.x, y: s.normal.y}
					s.active = &s.alt
					s.touchAll()
				} else {
					if s.active != &s.alt {
						return errors.New("PTY observer unpaired alternate-screen exit")
					}
					s.active = &s.normal
					s.charset, s.gl = s.savedCharset, s.savedGL
					// The normal buffer retains its saved cursor/pending wrap.
					// DECAWM is global and is not restored by DECRC.
					s.touchAll()
				}
			default:
				return fmt.Errorf("PTY observer unsupported private mode %d", value)
			}
		}
		return nil
	}
	b := s.active
	amount := max(1, values[0])
	switch final {
	case 'H', 'f':
		if len(values) > 2 {
			return errors.New("PTY observer invalid cursor position")
		}
		x := 0
		if len(values) == 2 {
			x = max(1, values[1]) - 1
		}
		b.x, b.y, b.wrap = min(s.w-1, x), min(s.h-1, amount-1), false
	case 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'd':
		if len(values) != 1 {
			return errors.New("PTY observer invalid cursor movement")
		}
		switch final {
		case 'A', 'F':
			b.y = max(0, b.y-amount)
		case 'B', 'E':
			b.y = min(s.h-1, b.y+amount)
		case 'C':
			b.x = min(s.w-1, b.x+amount)
		case 'D':
			b.x = max(0, b.x-amount)
		case 'G':
			b.x = min(s.w-1, amount-1)
		case 'd':
			b.y = min(s.h-1, amount-1)
		}
		if final == 'E' || final == 'F' {
			b.x = 0
		}
		b.wrap = false
	case 'J', 'K', 'X':
		if len(values) != 1 || final != 'X' && values[0] > 3 || final == 'K' && values[0] == 3 {
			return errors.New("PTY observer invalid erase operation")
		}
		start, end := b.y*s.w+b.x, len(b.cells)
		if final == 'K' || final == 'X' {
			end = (b.y + 1) * s.w
		}
		if final == 'X' {
			end = min(end, start+amount)
		} else {
			switch values[0] {
			case 1:
				end = start + 1
				start = 0
				if final == 'K' {
					start = b.y * s.w
				}
			case 2:
				start = 0
				if final == 'K' {
					start = b.y * s.w
				}
			case 3:
				return nil // Erase scrollback; this observer keeps no scrollback.
			}
		}
		s.revision++
		for i := start; i < end; i++ {
			s.clearCell(i)
		}
		b.wrap = false
	case 'm':
		return terminalRendition(values)
	case 't':
		if len(values) < 2 || len(values) > 3 || values[0] != 22 && values[0] != 23 || values[1] > 2 || len(values) == 3 && values[2] != 0 {
			return errors.New("PTY observer unsupported window operation")
		}
	case 'r':
		if len(values) > 2 || values[0] > 1 || len(values) == 2 && values[1] != 0 && values[1] != s.h {
			return errors.New("PTY observer unsupported scrolling region")
		}
		b.x, b.y, b.wrap = 0, 0, false
	default:
		return fmt.Errorf("PTY observer unsupported CSI final %q", final)
	}
	return nil
}

func terminalRendition(values []int) error {
	for i := 0; i < len(values); i++ {
		value := values[i]
		switch {
		case value == 0 || value >= 1 && value <= 7 || value == 9 || value >= 22 && value <= 25 || value == 27 || value == 29 || value >= 30 && value <= 37 || value == 39 || value >= 40 && value <= 47 || value == 49 || value == 59 || value >= 90 && value <= 97 || value >= 100 && value <= 107:
		case value == 38 || value == 48 || value == 58:
			count := 0
			if i+1 < len(values) && values[i+1] == 5 {
				count = 1
			} else if i+1 < len(values) && values[i+1] == 2 {
				count = 3
			}
			if count == 0 || i+1+count >= len(values) {
				return errors.New("PTY observer invalid extended colour")
			}
			for _, component := range values[i+2 : i+2+count] {
				if component > 255 {
					return errors.New("PTY observer colour outside byte range")
				}
			}
			i += 1 + count
		default:
			// In particular, concealed glyphs must not become visible matches.
			return fmt.Errorf("PTY observer unsupported rendition %d", value)
		}
	}
	return nil
}

func (s *terminalScreen) clearCell(i int) {
	b := s.active
	if b.cells[i].continuation && i%s.w > 0 {
		b.cells[i-1] = terminalCell{}
	} else if b.cells[i].width == 2 && i%s.w+1 < s.w {
		b.cells[i+1] = terminalCell{}
	}
	// An erased background blank has no emitted-space provenance. Only
	// put(' ') can satisfy a post-key fence for a literal space.
	b.cells[i] = terminalCell{}
	s.dirty[i/s.w] = true
}

func (s *terminalScreen) put(r rune) error {
	b := s.active
	width := uniseg.StringWidth(string(r))
	if width == 0 {
		x := b.x - 1
		if b.wrap {
			x = b.x
		}
		if x < 0 {
			return errors.New("PTY observer combining rune without a base")
		}
		i := b.y*s.w + x
		if b.cells[i].continuation {
			i--
		}
		cell := &b.cells[i]
		text := cell.text + string(r)
		if cell.text == "" || len(text) > 64 || uniseg.StringWidth(text) != cell.width {
			return errors.New("PTY observer unsupported grapheme cluster")
		}
		s.revision++
		// Appending a mark changes the display, not the generation of its base
		// glyph. The current phase words do not match combining-only text.
		cell.text = text
		s.dirty[b.y] = true
		return nil
	}
	if width != 1 && width != 2 {
		return errors.New("PTY observer unsupported glyph width")
	}
	if b.wrap || width == 2 && b.x == s.w-1 {
		if !s.autoWrap {
			if width == 2 {
				return errors.New("PTY observer wide glyph outside grid")
			}
		} else {
			b.x = 0
			s.lineFeed()
		}
	}
	s.revision++
	i := b.y*s.w + b.x
	s.clearCell(i)
	if width == 2 {
		s.clearCell(i + 1)
		b.cells[i+1] = terminalCell{continuation: true, written: s.revision}
	}
	b.cells[i] = terminalCell{text: string(r), width: width, written: s.revision}
	b.wrap = s.autoWrap && b.x+width >= s.w
	b.x = min(s.w-1, b.x+width)
	return nil
}

func (s *terminalScreen) lineFeed() {
	b := s.active
	b.wrap = false
	if b.y < s.h-1 {
		b.y++
		return
	}
	copy(b.cells, b.cells[s.w:])
	clear(b.cells[(s.h-1)*s.w:])
	s.touchAll()
}

func (s *terminalScreen) touchAll() {
	s.revision++
	// Scrolling/restoring moves existing glyphs without re-emitting them.
	// Notify visible-row observers while preserving their write generations.
	for y := range s.dirty {
		s.dirty[y] = true
	}
}

func (s *terminalScreen) contains(word string) bool {
	if word == "" || s.sync || len(s.pending) != 0 {
		return false
	}
	for y := 0; y < s.h; y++ {
		if strings.Contains(s.row(y), word) {
			return true
		}
	}
	return false
}

func (s *terminalScreen) row(y int) string {
	var text strings.Builder
	for _, cell := range s.active.cells[y*s.w : (y+1)*s.w] {
		if cell.continuation {
			continue
		}
		if cell.text == "" {
			text.WriteByte(' ')
		} else {
			text.WriteString(cell.text)
		}
	}
	return text.String()
}

func (s *terminalScreen) flushRows() {
	if s.sync {
		return
	}
	for y, dirty := range s.dirty {
		if dirty && s.observe != nil {
			s.observe(s.row(y))
		}
		s.dirty[y] = false
	}
}

// Every displayed glyph in a phase match must have been written after the
// preceding key. Cursor movement, style changes or an unrelated cell update
// cannot make a stale title satisfy the next phase. This is an output fence,
// not an application acknowledgement; the scenarios also alternate distinct
// titles, retaining the child draft/input assertions as independent evidence.
func (s *terminalScreen) containsAfter(word string, fence uint64) bool {
	if word == "" || s.sync || len(s.pending) != 0 {
		return false
	}
	for y := 0; y < s.h; y++ {
		row := s.row(y)
		for offset := 0; offset < len(row); {
			found := strings.Index(row[offset:], word)
			if found < 0 {
				break
			}
			start, end := offset+found, offset+found+len(word)
			fresh, position := true, 0
			for _, cell := range s.active.cells[y*s.w : (y+1)*s.w] {
				if cell.continuation {
					continue
				}
				length := max(1, len(cell.text))
				if position < end && position+length > start && cell.written <= fence {
					fresh = false
				}
				position += length
			}
			if fresh {
				return true
			}
			offset = start + 1
		}
	}
	return false
}

func TestTerminalScreenVisibleCellSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, wire, want, absent string
	}{
		{"wide blank backspace", "  \b\b中  \b\b文", "中文", "中  文"},
		{"backspace does not delete", "ab\bX", "aX", "abX"},
		{"separate cursor positions", "中\x1b[1;6H文", "中   文", "中文"},
		{"separate rows", "中\r\n文", "文", "中文"},
		{"erase line", "中文\r\x1b[2K完", "完", "中文"},
		{"overwrite wide tail", "中文\x1b[1;2HX", " X文", "中文"},
		{"overwrite wide head", "中文\rX", "X 文", "中文"},
		{"relative cursor", "abcd\x1b[2DX", "abXd", "abcd"},
		{"carriage return overwrite", "ab\rX", "Xb", "ab"},
		{"DEC line drawing", "\x1b(0lqk\x1b(B", "┌─┐", "lqk"},
		{"OSC is not displayed", "\x1b]2;中文\aASCII", "ASCII", "中文"},
		{"split OSC string terminator", "\x1b]2;中文\x1b\\ASCII", "ASCII", "中文"},
		{"keyboard protocols preserve text", "\x1b[>4;2m\x1b[>1u\x1b[?9001hASCII", "ASCII", "中文"},
		{"ASCII replacement cannot make Chinese", "??", "??", "中文"},
		{"overwritten fragments stay separate", "中\rX文", "X文", "中文"},
		{"combining rune", "e\u0301", "e\u0301", "?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTerminalScreenObserver(12, 3, nil)
			// One-byte chunks exercise split UTF-8, CSI and OSC boundaries.
			for _, b := range []byte(tc.wire) {
				if err := s.feed([]byte{b}); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.finish(); err != nil || !s.contains(tc.want) || s.contains(tc.absent) {
				t.Fatalf("visible cells disagree: row=%q, error=%v", s.row(0), err)
			}
		})
	}
}

func TestTerminalScreenWrapScrollAndErase(t *testing.T) {
	s := newTerminalScreenObserver(4, 2, nil)
	feed := func(wire string) {
		t.Helper()
		if err := s.feed([]byte(wire)); err != nil {
			t.Fatal(err)
		}
	}
	feed("abcdE")
	if s.row(0) != "abcd" || s.row(1) != "E   " || s.contains("abcdE") {
		t.Fatal("autowrap joined rows or moved before the right margin")
	}
	feed("\r\n中文")
	if s.row(0) != "E   " || s.row(1) != "中文" || s.contains("abcd") {
		t.Fatal("bottom-row line feed did not scroll the grid")
	}
	feed("\x1b[?7l\x1b[1;4HXY")
	if s.row(0) != "E  Y" || s.contains("XY") {
		t.Fatal("disabled autowrap did not overwrite the margin cell")
	}
	feed("\x1b[2;2H\x1b[K")
	if s.row(1) != "    " || s.contains("中文") {
		t.Fatal("erasing a wide continuation retained its glyph")
	}
}

func TestTerminalScreenGlyphWriteFenceCounterexamples(t *testing.T) {
	for _, tc := range []struct {
		name, before, change, word, rewrite string
	}{
		{"restored old title", "中文", "\x1b[?1049h\x1b[?1049l", "中文", "\x1b[H中文"},
		{"scrolled old title", "\x1b[2;1H中文", "\r\n", "中文", "\x1b[H中文"},
		{"new mark on stale base", "e", "\u0301", "e", "\re\u0301"},
		{"erased gap is not emitted space", "old", "\x1b[HMain\x1b[1;5H\x1b[X\x1b[1;6Hmenu", "Main menu", "\x1b[HMain menu"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTerminalScreenObserver(12, 2, nil)
			feed := func(wire string) {
				t.Helper()
				if err := s.feed([]byte(wire)); err != nil {
					t.Fatal(err)
				}
			}
			feed(tc.before)
			fence := s.revision
			feed(tc.change)
			if !s.contains(tc.word) || s.containsAfter(tc.word, fence) {
				t.Fatal("visible old/implied glyphs crossed the emitted-glyph fence")
			}
			feed(tc.rewrite)
			if !s.containsAfter(tc.word, fence) {
				t.Fatal("explicitly rewritten glyphs did not cross the fence")
			}
			if tc.name == "new mark on stale base" && !s.containsAfter("e\u0301", fence) {
				t.Fatal("fully rewritten base and mark did not cross the fence")
			}
		})
	}
}

func TestTerminalScreenAlternateCursorCharsetAndWrap(t *testing.T) {
	feed := func(s *terminalScreen, wire string) {
		t.Helper()
		if err := s.feed([]byte(wire)); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("entry inherits cursor without pending wrap", func(t *testing.T) {
		s := newTerminalScreenObserver(12, 3, nil)
		feed(s, "\x1b[2;3H\x1b[?1049hX")
		if s.row(0) != strings.Repeat(" ", 12) || s.row(1) != "  X         " {
			t.Fatal("alternate entry homed the inherited cursor")
		}
		feed(s, "\x1b[?1049lq")
		if s.row(1) != "  q         " || s.contains("X") {
			t.Fatal("alternate exit did not restore the normal cursor and grid")
		}
	})
	t.Run("G0 designation restored", func(t *testing.T) {
		s := newTerminalScreenObserver(12, 3, nil)
		feed(s, "A\x1b[?1049h\x1b(0q\x1b[?1049lq")
		if !s.contains("Aq") || s.contains("A─") || s.charset[0] != 'B' {
			t.Fatal("alternate exit retained the alternate G0 designation")
		}
	})
	t.Run("G1 designation and GL restored", func(t *testing.T) {
		s := newTerminalScreenObserver(12, 3, nil)
		feed(s, "\x1b)0\x0e\x1b[?1049h\x1b)B\x0f\x1b[?1049lq")
		if !s.contains("─") || s.contains("q") || s.charset[1] != '0' || s.gl != 1 {
			t.Fatal("alternate exit lost the saved G1/GL selection")
		}
	})
	t.Run("saved pending wrap and global DECAWM are separate", func(t *testing.T) {
		s := newTerminalScreenObserver(4, 2, nil)
		feed(s, "abcd\x1b[?1049h")
		if s.active.x != 3 || s.active.y != 0 || s.active.wrap {
			t.Fatal("alternate clear did not retain coordinates/reset pending wrap")
		}
		feed(s, "\x1b[?7l\x1b[?1049l")
		if s.autoWrap || !s.active.wrap || s.active.x != 3 {
			t.Fatal("alternate exit confused pending wrap with global DECAWM")
		}
		feed(s, "X")
		if s.row(0) != "abcX" || s.row(1) != "    " {
			t.Fatal("global disabled DECAWM was not retained after cursor restore")
		}
	})
}

func TestTerminalScreenObservationFenceAndSynchronizedDraw(t *testing.T) {
	seen := map[string]bool{}
	s := newTerminalScreenObserver(12, 3, func(row string) {
		for _, word := range []string{"中文", "旧页", "新页"} {
			if strings.Contains(row, word) {
				seen[word] = true
			}
		}
	})
	feed := func(wire string) {
		t.Helper()
		if err := s.feed([]byte(wire)); err != nil {
			t.Fatal(err)
		}
	}
	feed("中文")
	fence := s.revision
	feed("\x1b[2;1H无关\x1b[1;1H\x1b[31m")
	if s.containsAfter("中文", fence) {
		t.Fatal("unchanged title crossed an observation fence")
	}
	feed("中")
	if s.containsAfter("中文", fence) {
		t.Fatal("partly stale title crossed an observation fence")
	}
	feed("文")
	if !s.containsAfter("中文", fence) {
		t.Fatal("newly displayed title did not cross the fence")
	}
	feed("\x1b[?2026h\x1b[2J\x1b[H旧页")
	if s.containsAfter("旧页", 0) || seen["旧页"] {
		t.Fatal("incomplete synchronized redraw became visible evidence")
	}
	feed("\x1b[H新页\x1b[?2026l")
	if !s.containsAfter("新页", fence) || !seen["新页"] || seen["旧页"] {
		t.Fatal("synchronized redraw did not expose only its completed cells")
	}
	feed("\x1b[?1049h\x1b[HASCII\x1b[?1049l")
	if !s.containsAfter("新页", 0) || s.containsAfter("ASCII", 0) {
		t.Fatal("alternate screen did not restore the normal cells")
	}
	feed("\x1b[2J")
	if s.containsAfter("中文", 0) || !seen["中文"] || !seen["新页"] {
		t.Fatal("teardown changed previously observed display evidence")
	}
}

func TestTerminalScreenRejectsUnsupportedAndUnboundedState(t *testing.T) {
	for _, wire := range []string{"\x1b[8m中文", "\x1b[?6h", "\x1b[2;3r", "\x1b[4h", "\x1b[8;24;80t", "\x1b]52;c;value\a", "\x1bPpayload\x1b\\", "\x1b[99999C", "\xff", "\u200d", "\x1b[?7l\x1b[1;12H中", "e" + strings.Repeat("\u0301", 33), "\x1b]2;" + strings.Repeat("x", 4097), "\x1b[?1049h\x1b[?1049h", "\x1b[?1049l"} {
		s := newTerminalScreenObserver(12, 3, nil)
		if err := s.feed([]byte(wire)); err == nil {
			t.Fatalf("unsupported output accepted: %.80q", wire)
		}
	}
	for _, wire := range []string{"\x1b[", "\x1b]2;unfinished", "\xe4", "\x1b[?2026h"} {
		s := newTerminalScreenObserver(12, 3, nil)
		if err := s.feed([]byte(wire)); err != nil || s.finish() == nil {
			t.Fatalf("incomplete output was not retained/rejected at EOF: %q, %v", wire, err)
		}
	}
	s := newTerminalScreenObserver(12, 3, nil)
	if err := s.feed([]byte(strings.Repeat("x", (256<<10)+1))); err == nil {
		t.Fatal("wire bound was not enforced")
	}
}
