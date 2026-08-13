package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// doctorServer answers the reference-torrent calls the checks make.
func doctorServer(t *testing.T, authorized bool) *httptest.Server {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authorized {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"nope"}}`))
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/resource/"+doctorRid):
			_, _ = fmt.Fprintf(w, `{"id":%q,"name":"Sintel","multi_file":false,"size":100,"files_count":1,
				"file":{"id":"a","name":"s.mkv","path":"/s.mkv","type":"file","size":100,"media_format":"video"}}`, doctorRid)
		case strings.Contains(r.URL.Path, "/export/"):
			_, _ = fmt.Fprintf(w, `{"source":{"id":"a","name":"s.mkv","path":"/s.mkv","type":"file","size":100},
				"exports":{"download":{"url":"http://%s/dl/a"}}}`, r.Host)
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			_, _ = w.Write(make([]byte, 100))
		default:
			w.WriteHeader(404)
		}
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func doctorCommand(t *testing.T) *cli.Command {
	t.Helper()
	root := Root()
	for _, c := range root.Commands {
		if c.Name == "doctor" {
			return c
		}
	}
	t.Fatal("doctor command not registered")
	return nil
}

func runChecks(t *testing.T, srv *httptest.Server, extra ...string) []check {
	t.Helper()
	t.Setenv("WEBTOR_BACKEND", "direct")
	t.Setenv("WEBTOR_BASE_URL", srv.URL)
	t.Setenv("WEBTOR_DOWNLOAD_DIR", t.TempDir())

	var got []check
	cmd := doctorCommand(t)
	cmd.Action = func(ctx context.Context, c *cli.Command) error {
		got = runDoctor(ctx, c)
		return nil
	}
	args := append([]string{"doctor"}, extra...)
	if err := cmd.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	return got
}

func stateOf(checks []check, name string) (checkState, string) {
	for _, c := range checks {
		if c.Name == name {
			return c.State, c.Detail
		}
	}
	return "", "missing"
}

func TestDoctorHealthyBackend(t *testing.T) {
	checks := runChecks(t, doctorServer(t, true))

	if st, d := stateOf(checks, "configuration"); st != checkOK {
		t.Errorf("configuration = %v (%s)", st, d)
	}
	if st, d := stateOf(checks, "api"); st != checkOK || !strings.Contains(d, "round-trip") {
		t.Errorf("api = %v (%s)", st, d)
	}
	// A rest-api backend has no account surface: reported, not failed.
	if st, _ := stateOf(checks, "account"); st != checkWarn {
		t.Errorf("account = %v, want warn on a direct backend", st)
	}
	if st, d := stateOf(checks, "speed"); st == checkFail {
		t.Errorf("speed = %v (%s)", st, d)
	}
	if st, d := stateOf(checks, "download folder"); st != checkOK {
		t.Errorf("download folder = %v (%s)", st, d)
	}
	for _, c := range checks {
		if c.State == checkFail {
			t.Errorf("unexpected failure: %+v", c)
		}
	}
}

func TestDoctorReportsRejectedKey(t *testing.T) {
	checks := runChecks(t, doctorServer(t, false))
	st, d := stateOf(checks, "api")
	if st != checkFail || !strings.Contains(d, "rejected") {
		t.Fatalf("api = %v (%s), want a failure about the key", st, d)
	}
	// The run stops there: no point sampling speed through a dead key.
	if st, _ := stateOf(checks, "speed"); st != "" {
		t.Errorf("speed was checked after an auth failure: %v", st)
	}
}

func TestDoctorSkipsSpeedOnRequest(t *testing.T) {
	checks := runChecks(t, doctorServer(t, true), "--no-speed")
	if st, _ := stateOf(checks, "speed"); st != "" {
		t.Errorf("speed ran despite --no-speed: %v", st)
	}
}

func TestDoctorFlagsUnwritableFolder(t *testing.T) {
	dir := t.TempDir() + "/ro"
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEBTOR_DOWNLOAD_DIR", dir)
	cmd := doctorCommand(t)
	currentCfg = nil
	// outputBase reads the flag first; the env path goes through Resolve,
	// so drive folderCheck directly with the flag set.
	if err := cmd.Set("output", dir); err == nil {
		if c := folderCheck(cmd); c.State != checkFail {
			t.Errorf("read-only folder = %v (%s)", c.State, c.Detail)
		}
	}
}
