package picker

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

// The arrow-key picker: raw terminal mode on stdin, ANSI rendering on
// stderr. ↑/↓ (or j/k while the filter is empty) move, PgUp/PgDn jump,
// typing filters live, Backspace edits the filter, Tab marks an item in
// multi mode, Enter confirms, Esc clears the filter (or cancels when it is
// already empty), Ctrl-C cancels. Anything that prevents raw mode falls
// back to the numbered prompt.

// ErrCancelled is returned on Ctrl-C: leave the whole program.
var ErrCancelled = errors.New("cancelled")

// ErrBack is returned on Esc (with an empty filter): go one screen back.
// Screens without a parent treat it like ErrCancelled.
var ErrBack = errors.New("back")

// ErrTab is returned when Tab is pressed on a single-select screen — the
// application-wide "switch to the downloads screen" gesture. Multi-select
// keeps Tab for marking.
var ErrTab = errors.New("tab")

// ExtraHint, when set, contributes an application status fragment to every
// picker's hint line (e.g. "tab: downloads (2 active)").
var ExtraHint func() string

func extraHint() string {
	if ExtraHint == nil {
		return ""
	}
	if h := ExtraHint(); h != "" {
		return " · " + h
	}
	return ""
}

var errTUIUnavailable = errors.New("tui unavailable")

// Mouse reporting: presses and wheel in SGR encoding (coordinates survive
// wide terminals). Enabled only while a picker screen is on.
const (
	mouseOn  = "\x1b[?1000h\x1b[?1006h"
	mouseOff = "\x1b[?1006l\x1b[?1000l"
)

// termEvent is one decoded input item: a control/printable byte run, a CSI
// arrow, an SGR mouse press, a wheel step, or a cursor-position report.
type termEvent struct {
	kind  int // evBytes, evArrow, evMouse, evWheel, evDSR
	bytes []byte
	final byte // arrow letter or PgUp/PgDn digit
	x, y  int  // mouse coordinates (1-based)
	row   int  // DSR row
	up    bool // wheel direction
}

const (
	evBytes = iota
	evArrow
	evMouse
	evWheel
	evDSR
)

// lexEvents splits an input chunk into events. A read can glue keystrokes,
// mouse packets and the DSR reply together, so everything is scanned.
func lexEvents(buf []byte) []termEvent {
	var evs []termEvent
	i := 0
	for i < len(buf) {
		if buf[i] != 0x1b {
			j := i
			for j < len(buf) && buf[j] != 0x1b {
				j++
			}
			evs = append(evs, termEvent{kind: evBytes, bytes: buf[i:j]})
			i = j
			continue
		}
		if i+1 >= len(buf) || buf[i+1] != '[' {
			evs = append(evs, termEvent{kind: evBytes, bytes: []byte{0x1b}})
			i++
			continue
		}
		j := i + 2
		for j < len(buf) && (buf[j] < 0x40 || buf[j] > 0x7e) {
			j++
		}
		if j >= len(buf) {
			evs = append(evs, termEvent{kind: evBytes, bytes: []byte{0x1b}})
			break
		}
		params := string(buf[i+2 : j])
		switch buf[j] {
		case 'M', 'm':
			if strings.HasPrefix(params, "<") {
				var b, x, y int
				if _, err := fmt.Sscanf(params, "<%d;%d;%d", &b, &x, &y); err == nil {
					switch {
					case b == 64 || b == 65:
						if buf[j] == 'M' {
							evs = append(evs, termEvent{kind: evWheel, up: b == 64})
						}
					case b&3 == 0 && buf[j] == 'M': // left press
						evs = append(evs, termEvent{kind: evMouse, x: x, y: y})
					}
				}
			}
		case 'R':
			var row, col int
			if _, err := fmt.Sscanf(params, "%d;%d", &row, &col); err == nil {
				evs = append(evs, termEvent{kind: evDSR, row: row})
			}
		case 'A', 'B', 'H', 'F':
			evs = append(evs, termEvent{kind: evArrow, final: buf[j]})
		case '~':
			if params == "5" || params == "6" {
				evs = append(evs, termEvent{kind: evArrow, final: params[0]})
			}
		}
		i = j + 1
	}
	return evs
}

func tuiAvailable() bool {
	return os.Getenv("WEBTOR_PLAIN_PICKER") == "" && os.Getenv("TERM") != "dumb" &&
		term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}

type tuiState struct {
	items   []Item
	visible []int // indices into items, after filtering
	cursor  int   // position within visible
	offset  int   // viewport top within visible
	filter  string
	checked map[int]bool // original indices (multi mode)
	multi   bool
	title   string
	height   int // viewport rows
	width    int // terminal columns
	drawn    int // lines drawn by the previous frame
	frameTop int // terminal row of the frame's first line (0 = unknown)
}

// moveCursor applies an arrow/page event.
func (s *tuiState) moveCursor(final byte) {
	switch final {
	case 'A':
		s.cursor--
	case 'B':
		s.cursor++
	case 'H':
		s.cursor = 0
	case 'F':
		s.cursor = len(s.visible) - 1
	case '5':
		s.cursor -= s.height
	case '6':
		s.cursor += s.height
	}
	if s.cursor > len(s.visible)-1 {
		s.cursor = len(s.visible) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	s.clamp()
}

// clickRow maps a terminal row to a visible index, or -1. Rows are 1-based;
// the frame's first line is the header, items follow.
func (s *tuiState) clickRow(y int) int {
	if s.frameTop <= 0 {
		return -1
	}
	vi := s.offset + (y - s.frameTop - 1)
	end := min(s.offset+s.height, len(s.visible))
	if y <= s.frameTop || vi < s.offset || vi >= end {
		return -1
	}
	return vi
}

// fit trims s to the terminal width by display width (CJK counts double),
// with an ellipsis. A row that wraps would break the redraw arithmetic —
// the cursor-up rewind counts logical lines, the terminal counts visual
// ones — so every rendered line must fit in one row.
func (s *tuiState) fit(text string) string {
	if s.width <= 1 {
		return text
	}
	return runewidth.Truncate(text, s.width-1, "…")
}

// resize re-reads the terminal geometry; the viewport height and the
// per-line width limit follow it.
func (s *tuiState) resize(fd int) {
	w, h, err := term.GetSize(fd)
	if err != nil {
		return
	}
	s.width = w
	s.height = 15
	if h > 6 {
		s.height = min(15, h-4)
	}
	s.clamp()
}

func (s *tuiState) refilter() {
	prevOriginal := -1
	if s.cursor < len(s.visible) {
		prevOriginal = s.visible[s.cursor]
	}
	s.visible = s.visible[:0]
	for i, it := range s.items {
		if s.filter == "" || strings.Contains(strings.ToLower(it.Label), strings.ToLower(s.filter)) {
			s.visible = append(s.visible, i)
		}
	}
	s.cursor = 0
	for vi, oi := range s.visible {
		if oi == prevOriginal {
			s.cursor = vi
			break
		}
	}
	s.clamp()
}

func (s *tuiState) clamp() {
	if s.cursor >= len(s.visible) {
		s.cursor = len(s.visible) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if s.cursor >= s.offset+s.height {
		s.offset = s.cursor - s.height + 1
	}
	if s.offset < 0 {
		s.offset = 0
	}
}

func (s *tuiState) render(out *os.File) {
	var b strings.Builder
	// Rewind over the previous frame; every line ends with \x1b[K to clear
	// leftovers from longer earlier content.
	if s.drawn > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", s.drawn)
	}
	line := func(format string, a ...any) {
		b.WriteString("\r\x1b[K")
		b.WriteString(s.fit(fmt.Sprintf(format, a...)))
		b.WriteString("\r\n")
	}
	styled := func(prefix, text, suffix string) {
		b.WriteString("\r\x1b[K")
		b.WriteString(prefix)
		b.WriteString(s.fit(text))
		b.WriteString(suffix)
		b.WriteString("\r\n")
	}
	header := s.title
	if s.filter != "" {
		header += "  filter: " + s.filter
	}
	line("%s", header)
	end := min(s.offset+s.height, len(s.visible))
	for vi := s.offset; vi < end; vi++ {
		it := s.items[s.visible[vi]]
		mark := ""
		if s.multi {
			if s.checked[s.visible[vi]] {
				mark = "[x] "
			} else {
				mark = "[ ] "
			}
		}
		row := mark + it.Label
		if it.Detail != "" {
			row += "  (" + it.Detail + ")"
		}
		if vi == s.cursor {
			styled("\x1b[7m", "▸ "+row, "\x1b[0m")
		} else {
			line("  %s", row)
		}
	}
	if len(s.visible) == 0 {
		line("  (nothing matches %q)", s.filter)
	}
	scroll := ""
	if len(s.visible) > s.height {
		scroll = fmt.Sprintf(" · %d/%d", s.cursor+1, len(s.visible))
	}
	hint := "↑↓ move · enter select · type to filter · esc back · ^c quit" + extraHint() + scroll
	if s.multi {
		hint = "↑↓ move · tab mark · enter confirm · type to filter · esc back · ^c quit" + scroll
	}
	styled("\x1b[2m", hint, "\x1b[0m")

	s.drawn = 2 + max(end-s.offset, 1) // header + rows (or the empty note) + hint
	// Ask where the cursor ended up — the reply (ESC[row;colR) arrives on
	// stdin and pins the frame's absolute position for click mapping.
	b.WriteString("\x1b[6n")
	_, _ = out.WriteString(b.String())
}

// wipe erases the frame drawn by the previous render, so leaving a screen
// leaves no residue behind — the next screen (or plain output) starts where
// this one stood.
func (s *tuiState) wipe(out *os.File) {
	if s.drawn > 0 {
		_, _ = fmt.Fprintf(out, "\r\x1b[%dA\x1b[J", s.drawn)
		s.drawn = 0
	}
}

// tuiPick runs the interactive list and returns original indices. In single
// mode the slice has one element; in multi mode Enter returns the marked
// items, or the cursor row when nothing is marked. The frame erases itself
// on exit.
func tuiPick(title string, items []Item, def int, multi bool) ([]int, error) {
	in, out := os.Stdin, os.Stderr
	fd := int(in.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, errTUIUnavailable
	}
	s := &tuiState{items: items, multi: multi, title: title,
		height: 15, checked: map[int]bool{}}
	_, _ = out.WriteString(mouseOn)
	restore := func() {
		_, _ = out.WriteString(mouseOff)
		s.wipe(out)
		_ = term.Restore(fd, oldState)
	}
	defer restore()
	s.resize(fd)
	s.refilter()
	if def >= 0 && def < len(items) {
		s.cursor = def
		s.clamp()
	}

	confirmAt := func(vi int) ([]int, error) {
		if multi {
			var picked []int
			for _, oi := range s.visible {
				if s.checked[oi] {
					picked = append(picked, oi)
				}
			}
			for oi := range s.checked {
				if s.checked[oi] && !contains(picked, oi) {
					picked = append(picked, oi)
				}
			}
			if len(picked) == 0 {
				picked = []int{s.visible[vi]}
			}
			sortInts(picked)
			return picked, nil
		}
		return []int{s.visible[vi]}, nil
	}

	buf := make([]byte, 128)
	for {
		s.render(out)
		n, err := in.Read(buf)
		if err != nil || n == 0 {
			return nil, ErrCancelled
		}
		for _, ev := range lexEvents(buf[:n]) {
			switch ev.kind {
			case evDSR:
				s.frameTop = ev.row - s.drawn
			case evArrow:
				s.moveCursor(ev.final)
			case evWheel:
				if ev.up {
					s.moveCursor('A')
				} else {
					s.moveCursor('B')
				}
			case evMouse:
				if vi := s.clickRow(ev.y); vi >= 0 {
					s.cursor = vi
					s.clamp()
					if multi {
						oi := s.visible[vi]
						s.checked[oi] = !s.checked[oi]
					} else {
						return confirmAt(vi)
					}
				}
			case evBytes:
				for bi := 0; bi < len(ev.bytes); bi++ {
					c := ev.bytes[bi]
					switch {
					case c == 0x03: // Ctrl-C
						return nil, ErrCancelled
					case c == '\r' || c == '\n':
						if len(s.visible) == 0 {
							continue
						}
						return confirmAt(s.cursor)
					case c == '\t' && !multi:
						return nil, ErrTab
					case c == '\t' && multi:
						if len(s.visible) > 0 {
							oi := s.visible[s.cursor]
							s.checked[oi] = !s.checked[oi]
							if s.cursor < len(s.visible)-1 {
								s.cursor++
							}
							s.clamp()
						}
					case c == 0x7f || c == 0x08: // Backspace
						if s.filter != "" {
							_, size := utf8.DecodeLastRuneInString(s.filter)
							s.filter = s.filter[:len(s.filter)-size]
							s.refilter()
						}
					case c == 0x1b: // lone Esc from the lexer
						if s.filter != "" {
							s.filter = ""
							s.refilter()
							continue
						}
						return nil, ErrBack
					case s.filter == "" && (c == 'j' || c == 'k'):
						if c == 'j' {
							s.moveCursor('B')
						} else {
							s.moveCursor('A')
						}
					default:
						// Printable run: take the whole remaining slice as
						// filter input (multi-byte runes arrive together).
						txt := string(ev.bytes[bi:])
						changed := false
						for _, r := range txt {
							if unicode.IsPrint(r) {
								s.filter += string(r)
								changed = true
							}
						}
						if changed {
							s.refilter()
						}
						bi = len(ev.bytes)
					}
				}
			}
		}
	}
}

func contains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func sortInts(s []int) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// Show renders a read-only text screen in the picker's style: the lines are
// width-fitted, any key returns (Ctrl-C cancels), and the frame erases
// itself. Piped/plain environments print the lines and wait for Enter.
func Show(title string, lines []string) error {
	if !tuiAvailable() {
		_, _ = fmt.Fprintln(os.Stderr, title)
		for _, l := range lines {
			_, _ = fmt.Fprintln(os.Stderr, "  "+l)
		}
		_, err := ReadLine("press Enter to continue…")
		return err
	}
	in, out := os.Stdin, os.Stderr
	fd := int(in.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		_, rerr := ReadLine("press Enter to continue…")
		return rerr
	}
	s := &tuiState{title: title, height: 15}
	_, _ = out.WriteString(mouseOn)
	defer func() {
		_, _ = out.WriteString(mouseOff)
		s.wipe(out)
		_ = term.Restore(fd, oldState)
	}()
	s.resize(fd)

	var b strings.Builder
	write := func(text string) {
		b.WriteString("\r\x1b[K")
		b.WriteString(s.fit(text))
		b.WriteString("\r\n")
	}
	write(title)
	for _, l := range lines {
		write("  " + l)
	}
	b.WriteString("\r\x1b[K\x1b[2m")
	b.WriteString(s.fit("any key to go back"))
	b.WriteString("\x1b[0m\r\n")
	s.drawn = len(lines) + 2
	_, _ = out.WriteString(b.String())

	buf := make([]byte, 8)
	n, rerr := in.Read(buf)
	if rerr == nil && n > 0 && buf[0] == 0x03 {
		return ErrCancelled
	}
	return nil
}
