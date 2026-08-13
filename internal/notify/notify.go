// Package notify posts desktop notifications through whatever the platform
// already has — osascript on macOS, notify-send on Linux, PowerShell toasts
// on Windows. Nothing is installed and nothing is required: on a machine
// without the helper the call is a silent no-op, because a missing
// notification must never interrupt a download.
package notify

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// timeout bounds the helper: a wedged notification daemon must not keep a
// goroutine (or a process shutdown) waiting.
const timeout = 5 * time.Second

// Enabled reports whether notifications should be posted. WEBTOR_NO_NOTIFY
// silences them; so does a machine with no helper.
func Enabled() bool {
	if os.Getenv("WEBTOR_NO_NOTIFY") != "" {
		return false
	}
	return helper() != ""
}

// helper returns the platform command used to post notifications.
func helper() string {
	switch runtime.GOOS {
	case "darwin":
		if p, err := exec.LookPath("osascript"); err == nil {
			return p
		}
	case "windows":
		if p, err := exec.LookPath("powershell"); err == nil {
			return p
		}
	default:
		if p, err := exec.LookPath("notify-send"); err == nil {
			return p
		}
	}
	return ""
}

// Post shows a notification. Errors are deliberately swallowed: this is a
// courtesy, never a dependency.
func Post(title, body string) {
	h := helper()
	if h == "" || os.Getenv("WEBTOR_NO_NOTIFY") != "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		// AppleScript takes the strings as literals; quotes and backslashes
		// inside a torrent name would otherwise end the string early.
		script := "display notification " + appleQuote(body) +
			" with title " + appleQuote(title)
		cmd = exec.CommandContext(ctx, h, "-e", script)
	case "windows":
		cmd = exec.CommandContext(ctx, h, "-NoProfile", "-Command",
			"[reflection.assembly]::loadwithpartialname('System.Windows.Forms');"+
				"$n=New-Object System.Windows.Forms.NotifyIcon;"+
				"$n.Icon=[System.Drawing.SystemIcons]::Information;$n.Visible=$true;"+
				"$n.ShowBalloonTip(5000,"+psQuote(title)+","+psQuote(body)+
				",[System.Windows.Forms.ToolTipIcon]::Info)")
	default:
		cmd = exec.CommandContext(ctx, h, "--app-name=webtor", title, body)
	}
	_ = cmd.Run()
}

func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
