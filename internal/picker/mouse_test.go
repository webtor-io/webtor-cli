package picker

import (
	"testing"
)

func TestLexEvents(t *testing.T) {
	// A realistic glued chunk: DSR reply + wheel down + left click + "a".
	buf := []byte("\x1b[24;1R\x1b[<65;10;5M\x1b[<0;7;3M a")
	evs := lexEvents(buf)
	if len(evs) != 4 {
		t.Fatalf("events = %d: %+v", len(evs), evs)
	}
	if evs[0].kind != evDSR || evs[0].row != 24 {
		t.Errorf("dsr = %+v", evs[0])
	}
	if evs[1].kind != evWheel || evs[1].up {
		t.Errorf("wheel = %+v", evs[1])
	}
	if evs[2].kind != evMouse || evs[2].x != 7 || evs[2].y != 3 {
		t.Errorf("mouse = %+v", evs[2])
	}
	if evs[3].kind != evBytes || string(evs[3].bytes) != " a" {
		t.Errorf("bytes = %+v", evs[3])
	}
}

func TestLexEventsIgnoresReleaseAndKeepsKeys(t *testing.T) {
	evs := lexEvents([]byte("\x1b[<0;7;3m\r\x1b[A\x1b"))
	// release ignored; \r as bytes; arrow; lone esc as bytes
	if len(evs) != 3 {
		t.Fatalf("events = %d: %+v", len(evs), evs)
	}
	if evs[0].kind != evBytes || evs[0].bytes[0] != '\r' {
		t.Errorf("enter = %+v", evs[0])
	}
	if evs[1].kind != evArrow || evs[1].final != 'A' {
		t.Errorf("arrow = %+v", evs[1])
	}
	if evs[2].kind != evBytes || evs[2].bytes[0] != 0x1b {
		t.Errorf("esc = %+v", evs[2])
	}
}

func TestClickRowMapping(t *testing.T) {
	s := &tuiState{height: 15}
	s.items = items("a", "b", "c")
	s.visible = []int{0, 1, 2}
	s.drawn = 5 // header + 3 rows + hint
	// Frame ends at row 20 → DSR reply row 20 → top = 15: header at 15,
	// items at 16..18, hint at 19.
	s.frameTop = 20 - s.drawn
	for y, want := range map[int]int{15: -1, 16: 0, 17: 1, 18: 2, 19: -1, 3: -1} {
		if got := s.clickRow(y); got != want {
			t.Errorf("clickRow(%d) = %d, want %d", y, got, want)
		}
	}
	// Unknown frame position: clicks are ignored.
	s.frameTop = 0
	if s.clickRow(16) != -1 {
		t.Error("click accepted without a frame position")
	}
	// Scrolled viewport: offset shifts the mapping.
	s.frameTop = 15
	s.offset = 1
	s.visible = []int{0, 1, 2, 3}
	if got := s.clickRow(16); got != 1 {
		t.Errorf("scrolled clickRow(16) = %d, want 1", got)
	}
}
