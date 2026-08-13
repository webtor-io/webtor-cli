package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/config"
	"github.com/webtor-io/webtor-cli/internal/picker"
	"github.com/webtor-io/webtor-cli/internal/render"
)

// Background downloads for the interactive mode. Tab opens the downloads
// screen from any picker; a task opens its own action screen with pause /
// resume / abort. Paused tasks persist in downloads.json next to the config
// and reappear in the next session; running tasks are parked as paused on
// exit, so quitting never silently loses progress.

type dlStatus int32

const (
	dlRunning dlStatus = iota
	dlPaused
	dlDone
	dlFailed
	dlAborted
)

// dlSpec is everything needed to (re)start a task — also the on-disk format.
type dlSpec struct {
	Rid    string            `json:"rid"`
	Label  string            `json:"label"`
	Layout bool              `json:"layout"`
	Base   string            `json:"base"` // output directory at start time
	Files  []webtor.ListItem `json:"files"`
}

type dlTask struct {
	id     int
	spec   dlSpec
	total  int64
	done   atomic.Int64
	status atomic.Int32
	errMsg atomic.Value // string
	pause  atomic.Bool  // distinguishes pause from abort on ctx cancel
	cancel context.CancelFunc
}

func (t *dlTask) st() dlStatus { return dlStatus(t.status.Load()) }

func (t *dlTask) detail() string {
	pct := ""
	if t.total > 0 {
		pct = fmt.Sprintf("%d%% · %s of %s", t.done.Load()*100/t.total,
			render.Size(t.done.Load()), render.Size(t.total))
	} else {
		pct = render.Size(t.done.Load())
	}
	switch t.st() {
	case dlRunning:
		return pct
	case dlPaused:
		return "paused · " + pct
	case dlDone:
		return "done · " + render.Size(t.total)
	case dlAborted:
		return "aborted"
	default:
		msg, _ := t.errMsg.Load().(string)
		return "failed: " + msg
	}
}

type dlManager struct {
	mu     sync.Mutex
	tasks  []*dlTask
	seq    int
	loaded bool
}

var downloads dlManager

func dlStatePath() string { return filepath.Join(config.Dir(), "downloads.json") }

// ensureLoaded brings paused tasks from the previous session into the list.
func (m *dlManager) ensureLoaded() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loaded {
		return
	}
	m.loaded = true
	b, err := os.ReadFile(dlStatePath())
	if err != nil {
		return
	}
	var specs []dlSpec
	if json.Unmarshal(b, &specs) != nil {
		return
	}
	for _, sp := range specs {
		m.seq++
		t := &dlTask{id: m.seq, spec: sp, total: specTotal(sp), cancel: func() {}}
		t.status.Store(int32(dlPaused))
		t.done.Store(onDiskBytes(sp))
		m.tasks = append(m.tasks, t)
	}
}

func specTotal(sp dlSpec) int64 {
	var n int64
	for _, f := range sp.Files {
		n += f.Size
	}
	return n
}

// onDiskBytes counts what a paused task already has on disk.
func onDiskBytes(sp dlSpec) int64 {
	var n int64
	for _, f := range sp.Files {
		if st, err := os.Stat(destFor(sp, f)); err == nil && st.Size() <= f.Size {
			n += st.Size()
		}
	}
	return n
}

func destFor(sp dlSpec, f webtor.ListItem) string {
	if sp.Layout {
		return filepath.Join(sp.Base, filepath.FromSlash(strings.TrimPrefix(f.Path, "/")))
	}
	name := f.Name
	if name == "" {
		name = filepath.Base(f.Path)
	}
	if sp.Base == "" {
		return name
	}
	return filepath.Join(sp.Base, name)
}

// persist writes every pause-worthy task (paused + running) to disk.
func (m *dlManager) persist() {
	m.mu.Lock()
	var specs []dlSpec
	for _, t := range m.tasks {
		if s := t.st(); s == dlPaused || s == dlRunning {
			specs = append(specs, t.spec)
		}
	}
	m.mu.Unlock()
	if len(specs) == 0 {
		_ = os.Remove(dlStatePath())
		return
	}
	if b, err := json.Marshal(specs); err == nil {
		_ = os.MkdirAll(config.Dir(), 0o755)
		_ = os.WriteFile(dlStatePath(), b, 0o644)
	}
}

// ParkRunning marks running tasks paused (called on process exit): their
// specs are already persisted, the next session resumes from the bytes on
// disk.
func (m *dlManager) ParkRunning() {
	m.mu.Lock()
	for _, t := range m.tasks {
		if t.st() == dlRunning {
			t.pause.Store(true)
			t.cancel()
		}
	}
	m.mu.Unlock()
	m.persist()
}

func (m *dlManager) running() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, t := range m.tasks {
		if t.st() == dlRunning {
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
	for i, t := range m.tasks {
		if t.id == id {
			m.tasks = append(m.tasks[:i], m.tasks[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
	m.persist()
}

// start launches a background download and returns immediately.
func (m *dlManager) start(c *webtor.Client, sp dlSpec) {
	m.ensureLoaded()
	m.mu.Lock()
	m.seq++
	t := &dlTask{id: m.seq, spec: sp, total: specTotal(sp)}
	m.tasks = append(m.tasks, t)
	m.mu.Unlock()
	m.persist()
	m.run(c, t)
}

// resume restarts a paused task; the on-disk bytes are picked up by the
// per-file offset logic.
func (m *dlManager) resume(c *webtor.Client, t *dlTask) {
	if t.st() != dlPaused {
		return
	}
	t.pause.Store(false)
	t.done.Store(0)
	t.status.Store(int32(dlRunning))
	m.run(c, t)
}

func (m *dlManager) run(c *webtor.Client, t *dlTask) {
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	body := func() {
		defer cancel()
		for _, it := range t.spec.Files {
			if err := bgDownloadOne(ctx, c, t.spec.Rid, &it, destFor(t.spec, it), t); err != nil {
				switch {
				case ctx.Err() != nil && t.pause.Load():
					t.status.Store(int32(dlPaused))
				case ctx.Err() != nil:
					t.status.Store(int32(dlAborted))
				default:
					t.errMsg.Store(strings.TrimPrefix(err.Error(), "webtor: "))
					t.status.Store(int32(dlFailed))
				}
				m.persist()
				return
			}
		}
		t.status.Store(int32(dlDone))
		m.persist()
	}
	if os.Getenv("WEBTOR_SYNC_DOWNLOADS") != "" {
		body() // test hook: piped-answer scripts need deterministic completion
		return
	}
	go body()
}

// bgDownloadOne downloads one file with no terminal output: progress goes to
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

// downloadsHint feeds the pickers' status line: "tab: downloads (2 active)".
func downloadsHint() string {
	downloads.ensureLoaded()
	total := downloads.count()
	if total == 0 {
		return ""
	}
	if r := downloads.running(); r > 0 {
		return fmt.Sprintf("tab: downloads (%d active)", r)
	}
	return fmt.Sprintf("tab: downloads (%d)", total)
}

// downloadsScreen is the live progress list; a picked task opens its action
// screen. Esc goes back.
func downloadsScreen() error {
	downloads.ensureLoaded()
	for {
		tasks := downloads.snapshot()
		if len(tasks) == 0 {
			return picker.Show("Downloads:", []string{"nothing here — downloads you start land on this screen"})
		}
		n, err := picker.PickLive("Downloads:", func() []picker.Item {
			items := make([]picker.Item, 0, len(downloads.snapshot()))
			for _, t := range downloads.snapshot() {
				items = append(items, picker.Item{Label: t.spec.Label, Detail: t.detail()})
			}
			return items
		})
		if back(err) || errors.Is(err, picker.ErrTab) {
			// Tab toggles: from a menu it opens this screen, from here it
			// goes back to that menu.
			return nil
		}
		if err != nil {
			return err
		}
		tasks = downloads.snapshot()
		if n >= len(tasks) {
			continue
		}
		if err := downloadTaskScreen(tasks[n]); err != nil && !back(err) {
			if errors.Is(err, picker.ErrTab) {
				return nil
			}
			return err
		}
	}
}

// downloadTaskScreen is the per-task action menu: pause / resume / abort,
// or clear for a finished task.
func downloadTaskScreen(t *dlTask) error {
	for {
		var items []picker.Item
		switch t.st() {
		case dlRunning:
			items = []picker.Item{
				{Label: "pause", Detail: "keep the partial file, resume later"},
				{Label: "abort", Detail: "stop and forget (partial file stays on disk)"},
				{Label: "back"},
			}
		case dlPaused:
			items = []picker.Item{
				{Label: "resume", Detail: "continue from " + render.Size(onDiskBytes(t.spec))},
				{Label: "abort", Detail: "stop and forget (partial file stays on disk)"},
				{Label: "back"},
			}
		default:
			items = []picker.Item{
				{Label: "clear", Detail: "remove from the list"},
				{Label: "back"},
			}
		}
		n, err := picker.Pick(t.spec.Label+" — "+t.detail()+":", items, 0)
		if back(err) || errors.Is(err, picker.ErrTab) {
			// Esc goes up one screen; Tab toggles all the way back to the
			// menu the downloads flow was opened from.
			return picker.ErrTab
		}
		if err != nil {
			return err
		}
		switch items[n].Label {
		case "pause":
			t.pause.Store(true)
			t.cancel()
			downloads.persist()
			return nil
		case "resume":
			if currentClient == nil {
				return fmt.Errorf("no API client in this session")
			}
			downloads.resume(currentClient, t)
			return nil
		case "abort":
			if t.st() == dlRunning {
				t.pause.Store(false)
				t.cancel()
			} else {
				t.status.Store(int32(dlAborted))
			}
			downloads.remove(t.id)
			return nil
		case "clear":
			downloads.remove(t.id)
			return nil
		case "back":
			return nil
		}
	}
}
