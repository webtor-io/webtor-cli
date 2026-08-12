package picker

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

// The arrow-key picker: raw terminal mode on stdin, ANSI rendering on
// stderr. ↑/↓ (or j/k while the filter is empty) move, PgUp/PgDn jump,
// typing filters live, Backspace edits the filter, Tab marks an item in
// multi mode, Enter confirms, Esc clears the filter (or cancels when it is
// already empty), Ctrl-C cancels. Anything that prevents raw mode falls
// back to the numbered prompt.

// ErrCancelled is returned when the person backs out (Esc / Ctrl-C).
var ErrCancelled = errors.New("cancelled")

var errTUIUnavailable = errors.New("tui unavailable")

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
	height  int // viewport rows
	drawn   int // lines drawn by the previous frame
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
		fmt.Fprintf(&b, format, a...)
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
			line("\x1b[7m▸ %s\x1b[0m", row)
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
	hint := "↑↓ move · enter select · type to filter · esc cancel" + scroll
	if s.multi {
		hint = "↑↓ move · tab mark · enter confirm · type to filter · esc cancel" + scroll
	}
	line("\x1b[2m%s\x1b[0m", hint)

	s.drawn = 2 + max(end-s.offset, 1) // header + rows (or the empty note) + hint
	_, _ = out.WriteString(b.String())
}

// tuiPick runs the interactive list and returns original indices. In single
// mode the slice has one element; in multi mode Enter returns the marked
// items, or the cursor row when nothing is marked.
func tuiPick(title string, items []Item, def int, multi bool) ([]int, error) {
	in, out := os.Stdin, os.Stderr
	fd := int(in.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, errTUIUnavailable
	}
	restore := func() { _ = term.Restore(fd, oldState) }
	defer restore()

	height := 15
	if _, h, err := term.GetSize(fd); err == nil && h > 6 {
		height = min(height, h-4)
	}
	s := &tuiState{items: items, multi: multi, title: title,
		height: height, checked: map[int]bool{}}
	s.refilter()
	if def >= 0 && def < len(items) {
		s.cursor = def
		s.clamp()
	}

	buf := make([]byte, 64)
	for {
		s.render(out)
		n, err := in.Read(buf)
		if err != nil || n == 0 {
			return nil, ErrCancelled
		}
		key := buf[:n]
		switch {
		case key[0] == 0x03: // Ctrl-C
			return nil, ErrCancelled
		case key[0] == '\r' || key[0] == '\n':
			if len(s.visible) == 0 {
				continue
			}
			if multi {
				var picked []int
				for _, oi := range s.visible {
					if s.checked[oi] {
						picked = append(picked, oi)
					}
				}
				// Also count marks hidden by the current filter.
				for oi := range s.checked {
					if s.checked[oi] && !contains(picked, oi) {
						picked = append(picked, oi)
					}
				}
				if len(picked) == 0 {
					picked = []int{s.visible[s.cursor]}
				}
				sortInts(picked)
				return picked, nil
			}
			return []int{s.visible[s.cursor]}, nil
		case key[0] == '\t' && multi:
			if len(s.visible) > 0 {
				oi := s.visible[s.cursor]
				s.checked[oi] = !s.checked[oi]
				if s.cursor < len(s.visible)-1 {
					s.cursor++
				}
				s.clamp()
			}
		case key[0] == 0x7f || key[0] == 0x08: // Backspace
			if s.filter != "" {
				_, size := utf8.DecodeLastRuneInString(s.filter)
				s.filter = s.filter[:len(s.filter)-size]
				s.refilter()
			}
		case key[0] == 0x1b:
			if n == 1 { // lone Esc
				if s.filter != "" {
					s.filter = ""
					s.refilter()
					continue
				}
				return nil, ErrCancelled
			}
			if n >= 3 && key[1] == '[' {
				switch key[2] {
				case 'A':
					s.cursor--
				case 'B':
					s.cursor++
				case 'H':
					s.cursor = 0
				case 'F':
					s.cursor = len(s.visible) - 1
				case '5': // PgUp
					s.cursor -= s.height
				case '6': // PgDn
					s.cursor += s.height
				}
				if s.cursor < 0 {
					s.cursor = 0
				}
				if s.cursor > len(s.visible)-1 {
					s.cursor = len(s.visible) - 1
				}
				s.clamp()
			}
		default:
			txt := string(key)
			if s.filter == "" && (txt == "j" || txt == "k") { // vi motion
				if txt == "j" {
					s.cursor++
				} else {
					s.cursor--
				}
				if s.cursor < 0 {
					s.cursor = 0
				}
				if s.cursor > len(s.visible)-1 {
					s.cursor = len(s.visible) - 1
				}
				s.clamp()
				continue
			}
			for _, r := range txt {
				if unicode.IsPrint(r) {
					s.filter += string(r)
				}
			}
			s.refilter()
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
