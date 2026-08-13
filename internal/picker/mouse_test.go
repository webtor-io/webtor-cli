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

func TestLexEventsMotionIsHoverNotClick(t *testing.T) {
	// 35 = motion with no button (32|3), 32 = motion with the left button
	// held (a drag) — neither may register as a click.
	evs := lexEvents([]byte("\x1b[<35;9;4M\x1b[<32;9;5M"))
	if len(evs) != 2 {
		t.Fatalf("events = %d: %+v", len(evs), evs)
	}
	for i, ev := range evs {
		if ev.kind != evHover {
			t.Errorf("event %d kind = %d, want evHover", i, ev.kind)
		}
	}
	if evs[0].y != 4 || evs[1].y != 5 {
		t.Errorf("coordinates = %+v", evs)
	}
}

func TestHoverRenderUnderlinesRowUnderMouse(t *testing.T) {
	s := &tuiState{height: 15, hover: -1}
	s.items = items("alpha", "beta", "gamma")
	s.visible = []int{0, 1, 2}
	s.drawn = 5
	s.frameTop = 20 - s.drawn // header at 15, items at 16..18

	// A motion over the second row marks it hovered and asks for a redraw.
	if !s.setHover(17) {
		t.Fatal("setHover reported no change")
	}
	if s.hover != 1 {
		t.Fatalf("hover = %d, want 1", s.hover)
	}
	// The same row again changes nothing (no redraw churn on every motion).
	if s.setHover(17) {
		t.Error("repeated hover asked for a redraw")
	}
	// Leaving the list clears it.
	if !s.setHover(19) || s.hover != -1 {
		t.Errorf("hover after leaving = %d, want -1", s.hover)
	}
}

func TestStashKeepsTypedInputDropsReports(t *testing.T) {
	pendingInput = nil
	// A burst the terminal delivered in one read: a mouse packet, a typed
	// line, a cursor-position reply. Only the typed text may survive.
	stashEvents(lexEvents([]byte("\x1b[<35;5;7MNew Name\n\x1b[10;1R")))
	line, ok := takeStashedLine()
	if !ok || line != "New Name" {
		t.Fatalf("stashed line = %q, ok=%v", line, ok)
	}
	if len(pendingInput) != 0 {
		t.Errorf("leftovers after taking the line: %q", pendingInput)
	}
	// Without a newline there is no complete line to hand over yet.
	stashEvents(lexEvents([]byte("half")))
	if _, ok := takeStashedLine(); ok {
		t.Error("returned an unterminated line")
	}
	pendingInput = nil
}

// A burst larger than the read buffer splits an escape sequence in half.
// Feeding the halves separately must yield the same events as feeding the
// whole thing — and must never emit the tail as text or the head as ESC,
// which is what leaked "[50;1R" into filters and closed screens.
func TestLexEventsCarryAcrossSplit(t *testing.T) {
	whole := []byte("\x1b[<35;5;7M\x1b[10;1R")
	for cut := 1; cut < len(whole); cut++ {
		evs, carry := lexEventsCarry(whole[:cut], true)
		evs2, carry2 := lexEventsCarry(append(append([]byte(nil), carry...), whole[cut:]...), false)
		if len(carry2) != 0 {
			t.Fatalf("cut %d: carry left over: %q", cut, carry2)
		}
		all := append(evs, evs2...)
		var kinds []int
		for _, e := range all {
			kinds = append(kinds, e.kind)
		}
		if len(all) != 2 || kinds[0] != evHover || kinds[1] != evDSR {
			t.Errorf("cut %d: events = %v", cut, kinds)
		}
		for _, e := range all {
			if e.kind == evBytes {
				t.Errorf("cut %d: leaked text %q", cut, e.bytes)
			}
		}
	}
}

// A lone ESC keystroke (a short read) must still mean "go back", not wait
// for a continuation that never comes.
func TestLoneEscIsNotCarriedOnShortRead(t *testing.T) {
	evs, carry := lexEventsCarry([]byte{0x1b}, false)
	if len(carry) != 0 {
		t.Fatalf("carry = %q, want none", carry)
	}
	if len(evs) != 1 || evs[0].kind != evBytes || evs[0].bytes[0] != 0x1b {
		t.Fatalf("events = %+v", evs)
	}
	// The same byte at a full-buffer boundary is a split, so it is carried.
	evs, carry = lexEventsCarry([]byte{0x1b}, true)
	if len(evs) != 0 || len(carry) != 1 {
		t.Fatalf("boundary: events=%+v carry=%q", evs, carry)
	}
}
