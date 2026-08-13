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
	_, _ = out.WriteString(mouseOn)
	defer func() {
		_, _ = out.WriteString(mouseOff)
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
					return s.visible[vi], nil
				}
			case evBytes:
				for _, c := range ev.bytes {
					switch c {
					case 0x03:
						return 0, ErrCancelled
					case '\r', '\n':
						if len(s.visible) > 0 {
							return s.visible[s.cursor], nil
						}
					case 0x1b:
						return 0, ErrBack
					case '\t':
						return 0, ErrTab
					case 'j':
						s.moveCursor('B')
					case 'k':
						s.moveCursor('A')
					}
				}
			}
		}
	}
}
