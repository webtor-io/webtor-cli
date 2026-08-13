package cmd

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/webtor-io/webtor-cli/internal/picker"
	"github.com/webtor-io/webtor-cli/internal/render"
)

// While downloads run, the terminal itself reports them: the tab/window
// title carries the percentage, and terminals that understand the progress
// escape (Windows Terminal, WezTerm, Ghostty, ConEmu) draw a bar on the tab
// or taskbar entry. Terminals that do not simply ignore the sequence.
//
// Both are OSC strings, which never move the cursor, so they are safe to
// emit between frames — picker.LockedWrite keeps them out of the middle of
// one.

// termTitle sets the window/tab title (OSC 0).
func termTitle(s string) string { return "\x1b]0;" + s + "\x07" }

// termProgress drives the tab/taskbar indicator (OSC 9;4): state 0 clears
// it, 1 shows a determinate bar. The indeterminate state (3) is never used:
// it renders as a spinner that says nothing and, if anything ever fails to
// clear it, looks like the terminal is stuck forever.
func termProgress(state, percent int) string {
	if os.Getenv("WEBTOR_NO_PROGRESS") != "" {
		return ""
	}
	return fmt.Sprintf("\x1b]9;4;%d;%d\x07", state, percent)
}

// ClearTermProgress wipes the tab indicator. Called on startup as well as
// on exit: a previous run that was killed outright cannot clean up after
// itself, so the next one does it.
func ClearTermProgress() {
	if !render.IsTTY(os.Stderr) {
		return
	}
	picker.LockedWrite(os.Stderr, termProgress(0, 0))
}

type statusReporter struct {
	stop chan struct{}
	once sync.Once
	wg   sync.WaitGroup
}

// startStatusReporter keeps the terminal's title and progress indicator in
// step with the download manager until stop is called. It is a no-op when
// the output is not a terminal or the invocation asked for machine output.
func startStatusReporter(quiet bool) *statusReporter {
	if quiet || !render.IsTTY(os.Stderr) {
		return nil
	}
	r := &statusReporter{stop: make(chan struct{})}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		t := time.NewTicker(time.Second)
		defer t.Stop()
		dirty := false // something was written and needs clearing on exit
		for {
			select {
			case <-r.stop:
				// Unconditional: clearing an indicator nobody set is free,
				// leaving one behind is not.
				picker.LockedWrite(os.Stderr, termProgress(0, 0)+termTitle(""))
				return
			case <-t.C:
				if s, ok := downloadStatus(); ok {
					picker.LockedWrite(os.Stderr, s)
					dirty = true
				} else if dirty {
					picker.LockedWrite(os.Stderr, termProgress(0, 0)+termTitle(""))
					dirty = false
				}
			}
		}
	}()
	return r
}

func (r *statusReporter) Stop() {
	if r == nil {
		return
	}
	r.once.Do(func() { close(r.stop) })
	r.wg.Wait()
}

// downloadStatus renders the current state as terminal escapes, or reports
// false when nothing is running.
func downloadStatus() (string, bool) {
	tasks := downloads.snapshot()
	var running, done, total int64
	var names []string
	for _, t := range tasks {
		if t.st() != dlRunning {
			continue
		}
		running++
		done += t.done.Load()
		total += t.total
		names = append(names, t.spec.Label)
	}
	if running == 0 {
		return "", false
	}
	title := names[0]
	if running > 1 {
		title = fmt.Sprintf("%d downloads", running)
	}
	if total > 0 {
		pct := int(done * 100 / total)
		return termProgress(1, pct) +
			termTitle(fmt.Sprintf("↓ %d%% · %s · webtor", pct, trimTitle(title))), true
	}
	// No known size yet: the title carries the bytes, and the indicator is
	// left alone rather than spun.
	return termTitle(fmt.Sprintf("↓ %s · %s · webtor", render.Size(done), trimTitle(title))), true
}

// trimTitle keeps titles short enough to survive a narrow tab.
func trimTitle(s string) string {
	const max = 40
	if len(s) <= max {
		return s
	}
	return strings.TrimSpace(s[:max-1]) + "…"
}
