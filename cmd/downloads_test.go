package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	webtor "github.com/webtor-io/api-sdk-go"
)

const dlTestRid = "08ada5a7a6183aae1e09d831df6748d566095a10"

// slowServer trickles a 200000-byte payload so pause lands mid-transfer.
func slowServer(t *testing.T) *webtor.Client {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/export/"):
			_, _ = fmt.Fprintf(w, `{"source":{"id":"a","name":"big.bin","path":"/big.bin","type":"file","size":200000},
				"exports":{"download":{"url":"http://%s/dl/a"}}}`, r.Host)
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			start := 0
			if rng := r.Header.Get("Range"); rng != "" {
				_, _ = fmt.Sscanf(rng, "bytes=%d-", &start)
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-199999/200000", start))
				w.WriteHeader(206)
			}
			chunk := make([]byte, 10000)
			for i := start; i < 200000; i += len(chunk) {
				if _, err := w.Write(chunk[:min(len(chunk), 200000-i)]); err != nil {
					return
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				time.Sleep(20 * time.Millisecond)
			}
		default:
			w.WriteHeader(404)
		}
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	b, err := webtor.Direct(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	c, err := webtor.New(b)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The full pause lifecycle: pause mid-transfer persists the spec and keeps
// the partial file; a fresh "session" sees the paused task with its on-disk
// bytes; resume completes it byte-perfectly and clears the state file.
func TestPauseResumeAcrossSessions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dest := t.TempDir()
	c := slowServer(t)
	downloads = dlManager{}

	spec := dlSpec{Rid: dlTestRid, Label: "big.bin", Base: dest,
		Files: []webtor.ListItem{{ID: "a", Name: "big.bin", Path: "/big.bin", Size: 200000}}}
	downloads.start(c, spec)
	tasks := downloads.snapshot()
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d", len(tasks))
	}
	task := tasks[0]

	waitFor(t, "some progress", func() bool { return task.done.Load() > 20000 })
	task.stop(true)
	if task.st() != dlPaused {
		t.Fatalf("status after stop = %v", task.st())
	}

	if _, err := os.Stat(dlStatePath()); err != nil {
		t.Fatalf("state file missing after pause: %v", err)
	}
	st, err := os.Stat(filepath.Join(dest, "big.bin"))
	if err != nil || st.Size() == 0 || st.Size() >= 200000 {
		t.Fatalf("partial file wrong: %v size=%d", err, st.Size())
	}

	// New session: a fresh manager loads the paused task from disk.
	downloads = dlManager{}
	downloads.ensureLoaded()
	tasks = downloads.snapshot()
	if len(tasks) != 1 || tasks[0].st() != dlPaused {
		t.Fatalf("reloaded tasks = %+v", tasks)
	}
	if tasks[0].done.Load() != st.Size() {
		t.Fatalf("reloaded done = %d, on disk %d", tasks[0].done.Load(), st.Size())
	}

	downloads.resume(c, tasks[0])
	waitFor(t, "completion", func() bool { return tasks[0].st() == dlDone })
	fi, err := os.Stat(filepath.Join(dest, "big.bin"))
	if err != nil || fi.Size() != 200000 {
		t.Fatalf("final file: %v size=%d", err, fi.Size())
	}
	<-tasks[0].stopped // the goroutine's final persist has run
	if _, err := os.Stat(dlStatePath()); !os.IsNotExist(err) {
		t.Fatalf("state file should be gone after completion, got %v", err)
	}
}

// Quitting with a running task parks it as paused on disk.
func TestParkRunningPersists(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dest := t.TempDir()
	c := slowServer(t)
	downloads = dlManager{}

	downloads.start(c, dlSpec{Rid: dlTestRid, Label: "big.bin", Base: dest,
		Files: []webtor.ListItem{{ID: "a", Name: "big.bin", Path: "/big.bin", Size: 200000}}})
	task := downloads.snapshot()[0]
	waitFor(t, "progress", func() bool { return task.done.Load() > 10000 })

	downloads.ParkRunning() // waits for the goroutines itself
	if task.st() != dlPaused {
		t.Fatalf("status after park = %v", task.st())
	}
	b, err := os.ReadFile(dlStatePath())
	if err != nil || !strings.Contains(string(b), "big.bin") {
		t.Fatalf("state after park: %v %s", err, b)
	}
}

// Abort removes the task and its persisted spec.
func TestAbortForgets(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dest := t.TempDir()
	c := slowServer(t)
	downloads = dlManager{}

	downloads.start(c, dlSpec{Rid: dlTestRid, Label: "big.bin", Base: dest,
		Files: []webtor.ListItem{{ID: "a", Name: "big.bin", Path: "/big.bin", Size: 200000}}})
	task := downloads.snapshot()[0]
	waitFor(t, "progress", func() bool { return task.done.Load() > 10000 })
	task.stop(false) // abort path
	if task.st() != dlAborted {
		t.Fatalf("status after abort = %v", task.st())
	}
	downloads.remove(task.id)
	if downloads.count() != 0 {
		t.Fatal("task not removed")
	}
	if _, err := os.Stat(dlStatePath()); !os.IsNotExist(err) {
		t.Fatal("state file should be gone after abort")
	}
}

// One torrent gets one task: a second start while the first is unfinished
// returns the existing task instead of queueing a rival that would write
// the same files and hammer the same swarm.
func TestStartDeduplicatesPerTorrent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dest := t.TempDir()
	c := slowServer(t)
	downloads = dlManager{}

	spec := dlSpec{Rid: dlTestRid, Label: "whole torrent", Base: dest,
		Files: []webtor.ListItem{{ID: "a", Name: "big.bin", Path: "/big.bin", Size: 200000}}}
	first, started := downloads.start(c, spec)
	if !started {
		t.Fatal("first start was refused")
	}
	waitFor(t, "progress", func() bool { return first.done.Load() > 10000 })

	// A different selection of the same torrent still maps to the one task.
	again, started := downloads.start(c, dlSpec{Rid: dlTestRid, Label: "one file", Base: dest,
		Files: []webtor.ListItem{{ID: "a", Name: "big.bin", Path: "/big.bin", Size: 200000}}})
	if started {
		t.Error("a second task was queued for the same torrent")
	}
	if again != first {
		t.Error("the existing task was not returned")
	}
	if downloads.count() != 1 {
		t.Fatalf("tasks = %d, want 1", downloads.count())
	}

	// Paused counts as unfinished — the partial file is still ours.
	first.stop(true)
	_, started = downloads.start(c, spec)
	if started {
		t.Error("a task was queued while a paused one existed")
	}

	// Once it is out of the way, a fresh start is allowed again.
	downloads.remove(first.id)
	third, started := downloads.start(c, spec)
	if !started {
		t.Fatal("start refused after the previous task was removed")
	}
	third.stop(false)
}

// A different torrent is unaffected by the rule.
func TestStartAllowsOtherTorrents(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dest := t.TempDir()
	c := slowServer(t)
	downloads = dlManager{}

	a, _ := downloads.start(c, dlSpec{Rid: dlTestRid, Label: "a", Base: dest,
		Files: []webtor.ListItem{{ID: "a", Name: "a.bin", Path: "/a.bin", Size: 200000}}})
	b, started := downloads.start(c, dlSpec{Rid: "1111111111111111111111111111111111111111",
		Label: "b", Base: dest,
		Files: []webtor.ListItem{{ID: "a", Name: "b.bin", Path: "/b.bin", Size: 200000}}})
	if !started || downloads.count() != 2 {
		t.Fatalf("second torrent refused: started=%v count=%d", started, downloads.count())
	}
	a.stop(false)
	b.stop(false)
}
