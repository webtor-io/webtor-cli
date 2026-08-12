package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
	"github.com/webtor-io/webtor-cli/cmd"
	"github.com/webtor-io/webtor-cli/internal/testapi"
)

// TestMain registers the "webtor" testscript command: scripts run the real
// CLI (in a re-exec of the test binary) against an in-process fake API.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"webtor": func() { os.Exit(cmd.Main(context.Background(), os.Args)) },
	})
}

func TestScripts(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: filepath.Join("testdata", "script"),
		Setup: func(env *testscript.Env) error {
			// Two fakes, one per dialect; scripts pick via env.
			webui := httptest.NewServer(testapi.New("webui"))
			restapi := httptest.NewServer(testapi.New("restapi"))
			env.Defer(webui.Close)
			env.Defer(restapi.Close)
			env.Setenv("WEBUI_URL", webui.URL)
			env.Setenv("RESTAPI_URL", restapi.URL)
			env.Setenv("VALID_KEY", testapi.ValidKey)
			// Hermetic config location inside the script's work dir, and no
			// OS keyring — scripts must never touch the real keychain.
			env.Setenv("XDG_CONFIG_HOME", filepath.Join(env.WorkDir, ".config"))
			env.Setenv("WEBTOR_NO_KEYRING", "1")
			return nil
		},
	})
}
