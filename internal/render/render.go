// Package render produces the CLI's output: aligned human tables on a TTY,
// raw JSON with --json, and progress that degrades to plain lines when piped.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"
)

// JSON writes v as indented JSON to w — the machine half of every command.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// JSONError mirrors the API's error envelope on stderr so scripts parse one
// shape for success and failure alike.
func JSONError(w io.Writer, code, message string) {
	_ = JSON(w, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// IsTTY reports whether f is an interactive terminal.
func IsTTY(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

// Table renders rows with aligned columns. header may be nil.
func Table(w io.Writer, header []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	if len(header) > 0 {
		_, _ = fmt.Fprintln(tw, strings.Join(header, "\t"))
	}
	for _, r := range rows {
		_, _ = fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	_ = tw.Flush()
}

// Size humanizes a byte count ("1.4 GB"). Sizes under 1 KB print as bytes.
func Size(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for u := n / unit; u >= unit; u /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}
