package picker

import (
	"os"

	"golang.org/x/term"
)

// PickLive is Pick for data that changes underneath: refresh is called
// before every frame (and every ~500ms of idle time when the platform
// supports polling reads), so progress-style screens stay current without
// keystrokes. Enter returns the selected index into the LAST refreshed
// slice, Esc returns ErrBack, Ctrl-C ErrCancelled. Plain environments fall
// back to a one-shot numbered prompt over a snapshot.
func PickLive(title string, refresh func() []Item) (int, error) {
	if !tuiAvailable() {
		return promptPick(os.Stdin, os.Stderr, title, refresh(), -1)
	}
	in, out := os.Stdin, os.Stderr
	fd := int(in.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return promptPick(os.Stdin, os.Stderr, title, refresh(), -1)
	}
	polling := setPollRead(fd)
	s := &tuiState{items: refresh(), title: title, height: 15, checked: map[int]bool{}}
	defer func() {
		s.wipe(out)
		_ = term.Restore(fd, oldState)
	}()
	s.resize(fd)
	s.refilter()

	buf := make([]byte, 64)
	for {
		s.items = refresh()
		s.refilter() // keeps the cursor on the same original index
		s.render(out)
		n, rerr := in.Read(buf)
		if n == 0 {
			// A zero-byte read is the ~100ms poll timeout; Go surfaces it as
			// io.EOF on some platforms, so with polling active any empty read
			// is a tick, not a hangup (a raw-mode Ctrl-D arrives as byte 0x04,
			// never as EOF).
			if polling {
				continue
			}
			return 0, ErrCancelled
		}
		if rerr != nil {
			return 0, ErrCancelled
		}
		key := buf[:n]
		switch {
		case key[0] == 0x03:
			return 0, ErrCancelled
		case key[0] == '\r' || key[0] == '\n':
			if len(s.visible) == 0 {
				continue
			}
			return s.visible[s.cursor], nil
		case key[0] == 0x1b:
			if n == 1 {
				return 0, ErrBack
			}
			if n >= 3 && key[1] == '[' {
				switch key[2] {
				case 'A':
					s.cursor--
				case 'B':
					s.cursor++
				}
				if s.cursor < 0 {
					s.cursor = 0
				}
				if s.cursor > len(s.visible)-1 {
					s.cursor = len(s.visible) - 1
				}
				s.clamp()
			}
		case key[0] == 'j':
			s.cursor++
			if s.cursor > len(s.visible)-1 {
				s.cursor = len(s.visible) - 1
			}
			s.clamp()
		case key[0] == 'k':
			s.cursor--
			if s.cursor < 0 {
				s.cursor = 0
			}
			s.clamp()
		}
	}
}
