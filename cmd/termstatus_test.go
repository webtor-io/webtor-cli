package cmd

import (
	"strings"
	"sync/atomic"
	"testing"

	webtor "github.com/webtor-io/api-sdk-go"
)

func fakeTask(label string, done, total int64, st dlStatus) *dlTask {
	t := &dlTask{spec: dlSpec{Label: label, Rid: dlTestRid,
		Files: []webtor.ListItem{{Size: total}}}, total: total}
	t.done.Store(done)
	t.status.Store(int32(st))
	return t
}

func TestDownloadStatusEscapes(t *testing.T) {
	downloads = dlManager{loaded: true}

	// Nothing running: no status at all, so the terminal is left alone.
	if s, ok := downloadStatus(); ok {
		t.Fatalf("status with no tasks: %q", s)
	}

	// One running task: a determinate bar and the percentage in the title.
	downloads.tasks = []*dlTask{fakeTask("Sintel.mkv", 25, 100, dlRunning)}
	s, ok := downloadStatus()
	if !ok {
		t.Fatal("no status while a task runs")
	}
	if !strings.Contains(s, "\x1b]9;4;1;25\x07") {
		t.Errorf("progress escape missing: %q", s)
	}
	if !strings.Contains(s, "\x1b]0;↓ 25% · Sintel.mkv · webtor\x07") {
		t.Errorf("title wrong: %q", s)
	}

	// A finished task no longer counts.
	downloads.tasks[0].status.Store(int32(dlDone))
	if _, ok := downloadStatus(); ok {
		t.Error("finished task still reported")
	}

	// Several running tasks aggregate into one percentage.
	downloads.tasks = []*dlTask{
		fakeTask("a", 50, 100, dlRunning),
		fakeTask("b", 10, 100, dlRunning),
	}
	s, _ = downloadStatus()
	if !strings.Contains(s, "\x1b]9;4;1;30\x07") || !strings.Contains(s, "2 downloads") {
		t.Errorf("aggregate status wrong: %q", s)
	}

	// Unknown size: indeterminate bar, bytes instead of a percentage.
	unknown := fakeTask("c", 2048, 0, dlRunning)
	unknown.total = 0
	downloads.tasks = []*dlTask{unknown}
	s, _ = downloadStatus()
	if !strings.Contains(s, "\x1b]9;4;3;0\x07") || !strings.Contains(s, "2.0 kB") {
		t.Errorf("indeterminate status wrong: %q", s)
	}

	downloads = dlManager{}
}

func TestTrimTitle(t *testing.T) {
	long := strings.Repeat("x", 60)
	got := trimTitle(long)
	if len([]rune(got)) != 40 || !strings.HasSuffix(got, "…") {
		t.Errorf("trimTitle = %q (%d runes)", got, len([]rune(got)))
	}
	if trimTitle("short") != "short" {
		t.Error("short titles must pass through")
	}
}

// The reporter must stay silent when the output is not a terminal — piping
// the CLI somewhere should never inject escape sequences into that stream.
func TestStatusReporterSkipsNonTTY(t *testing.T) {
	if r := startStatusReporter(false); r != nil {
		r.Stop()
		t.Error("reporter started without a terminal")
	}
	if r := startStatusReporter(true); r != nil {
		r.Stop()
		t.Error("reporter started despite quiet mode")
	}
	// Stop on a nil reporter is a no-op, not a panic.
	var nilr *statusReporter
	nilr.Stop()
	_ = atomic.LoadInt32(new(int32))
}
