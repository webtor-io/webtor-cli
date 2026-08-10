package render

import (
	"fmt"
	"io"

	"github.com/schollz/progressbar/v3"
)

// NewProgress returns a writer that renders transfer progress to out as the
// payload is copied through it: a live bar on a TTY, plain milestone lines
// (every 10%) when piped, nothing when quiet. Call the returned finish when
// the copy is done.
func NewProgress(out io.Writer, label string, size int64, tty, quiet bool) (io.Writer, func()) {
	switch {
	case quiet:
		return io.Discard, func() {}
	case tty:
		bar := progressbar.NewOptions64(size,
			progressbar.OptionSetWriter(out),
			progressbar.OptionSetDescription(label),
			progressbar.OptionShowBytes(true),
			progressbar.OptionSetWidth(30),
			progressbar.OptionThrottle(100e6),
			progressbar.OptionOnCompletion(func() { _, _ = fmt.Fprintln(out) }),
		)
		return bar, func() { _ = bar.Finish() }
	default:
		return &milestoneWriter{out: out, label: label, size: size}, func() {}
	}
}

// milestoneWriter prints a line at every 10% step — readable in CI logs where
// a control-character bar is noise.
type milestoneWriter struct {
	out   io.Writer
	label string
	size  int64
	done  int64
	last  int
}

func (m *milestoneWriter) Write(p []byte) (int, error) {
	m.done += int64(len(p))
	if m.size > 0 {
		if pct := int(m.done * 100 / m.size); pct/10 > m.last/10 {
			m.last = pct
			_, _ = fmt.Fprintf(m.out, "%s: %d%% (%s / %s)\n", m.label, pct, Size(m.done), Size(m.size))
		}
	}
	return len(p), nil
}
