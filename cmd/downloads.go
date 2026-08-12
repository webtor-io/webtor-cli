package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/picker"
	"github.com/webtor-io/webtor-cli/internal/render"
)

// Background downloads for the interactive mode: starting one returns to the
// menu immediately, every menu grows a "downloads" entry while tasks exist,
// and the downloads screen shows live progress with per-task cancel.

type dlStatus int32

const (
	dlRunning dlStatus = iota
	dlDone
	dlFailed
	dlCancelled
)

type dlTask struct {
	id     int
	label  string
	total  int64
	done   atomic.Int64
	status atomic.Int32
	errMsg atomic.Value // string, set on failure
	cancel context.CancelFunc
}

func (t *dlTask) detail() string {
	switch dlStatus(t.status.Load()) {
	case dlRunning:
		if t.total > 0 {
			return fmt.Sprintf("%d%% · %s of %s", t.done.Load()*100/t.total,
				render.Size(t.done.Load()), render.Size(t.total))
		}
		return render.Size(t.done.Load())
	case dlDone:
		return "done · " + render.Size(t.total)
	case dlCancelled:
		return "cancelled"
	default:
		msg, _ := t.errMsg.Load().(string)
		return "failed: " + msg
	}
}

type dlManager struct {
	mu    sync.Mutex
	tasks []*dlTask
	seq   int
}

var downloads dlManager

func (m *dlManager) running() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, t := range m.tasks {
		if dlStatus(t.status.Load()) == dlRunning {
			n++
		}
	}
	return n
}

func (m *dlManager) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.tasks)
}

func (m *dlManager) snapshot() []*dlTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*dlTask(nil), m.tasks...)
}

func (m *dlManager) remove(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, t := range m.tasks {
		if t.id == id {
			m.tasks = append(m.tasks[:i], m.tasks[i+1:]...)
			return
		}
	}
}

// start launches a background download of files (torrent layout when layout)
// and returns immediately. The task's progress is read by the downloads
// screen; ordinary API errors mark the task failed instead of surfacing.
func (m *dlManager) start(cmd *cli.Command, c *webtor.Client, rid, label string, files []webtor.ListItem, layout bool) {
	var total int64
	for _, f := range files {
		total += f.Size
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.seq++
	t := &dlTask{id: m.seq, label: label, total: total, cancel: cancel}
	m.tasks = append(m.tasks, t)
	m.mu.Unlock()

	base := outputBase(cmd)
	run := func() {
		defer cancel()
		for _, it := range files {
			var dest string
			if layout {
				dest = filepath.Join(base, filepath.FromSlash(strings.TrimPrefix(it.Path, "/")))
			} else {
				name := it.Name
				if name == "" {
					name = filepath.Base(it.Path)
				}
				dest = destPath(cmd, name)
			}
			if err := bgDownloadOne(ctx, c, rid, &it, dest, t); err != nil {
				if ctx.Err() != nil {
					t.status.Store(int32(dlCancelled))
				} else {
					t.errMsg.Store(strings.TrimPrefix(err.Error(), "webtor: "))
					t.status.Store(int32(dlFailed))
				}
				return
			}
		}
		t.status.Store(int32(dlDone))
	}
	if os.Getenv("WEBTOR_SYNC_DOWNLOADS") != "" {
		// Test hook: piped-answer scripts need deterministic completion.
		run()
		return
	}
	go run()
}

// bgDownloadOne is downloadOne without any terminal output: progress goes to
// the task counters, complete local files count instantly, partial ones
// resume.
func bgDownloadOne(ctx context.Context, c *webtor.Client, rid string, item *webtor.ListItem, dest string, t *dlTask) error {
	var offset int64
	if st, err := os.Stat(dest); err == nil {
		switch {
		case st.Size() == item.Size:
			t.done.Add(item.Size)
			return nil
		case st.Size() < item.Size:
			offset = st.Size()
			t.done.Add(offset)
		}
	}
	if dir := filepath.Dir(dest); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	d, err := c.OpenDownload(ctx, rid, item.ID, webtor.WithOffset(offset))
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(io.MultiWriter(f, taskCounter{t}), d)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

type taskCounter struct{ t *dlTask }

func (w taskCounter) Write(p []byte) (int, error) {
	w.t.done.Add(int64(len(p)))
	return len(p), nil
}

// downloadsLabel is the menu entry text while tasks exist, or "" when the
// entry should not be shown.
func downloadsLabel() string {
	total := downloads.count()
	if total == 0 {
		return ""
	}
	if r := downloads.running(); r > 0 {
		return fmt.Sprintf("downloads (%d active)", r)
	}
	return "downloads (finished)"
}

// downloadsScreen is the live progress list: Enter cancels a running task
// (confirmed) or clears a finished one, Esc goes back.
func downloadsScreen() error {
	for {
		tasks := downloads.snapshot()
		if len(tasks) == 0 {
			return nil
		}
		n, err := picker.PickLive("Downloads:", func() []picker.Item {
			items := make([]picker.Item, 0, len(downloads.snapshot()))
			for _, t := range downloads.snapshot() {
				items = append(items, picker.Item{Label: t.label, Detail: t.detail()})
			}
			return items
		})
		if back(err) {
			return nil
		}
		if err != nil {
			return err
		}
		tasks = downloads.snapshot()
		if n >= len(tasks) {
			continue
		}
		t := tasks[n]
		if dlStatus(t.status.Load()) == dlRunning {
			if confirm(fmt.Sprintf("Cancel downloading %q?", t.label)) {
				t.cancel()
			}
			continue
		}
		downloads.remove(t.id)
	}
}
