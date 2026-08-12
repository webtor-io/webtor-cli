// Package picker is the CLI's interactive list chooser: a numbered table on
// stderr, a prompt on stdin. Typing a number picks, typing text filters the
// list (case-insensitive substring), an empty answer takes the default. No
// alternate screen, no raw mode — it works in any terminal and is scriptable
// by piping answers, which is also how the test suite drives it.
package picker

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Item is one selectable row.
type Item struct {
	Label  string
	Detail string
}

// prompter runs the shared read-filter loop; pick parses an answer against
// the currently visible items (visible[i] = original index).
func prompt(in io.Reader, out io.Writer, title string, items []Item, def string,
	pick func(answer string, visible []int) ([]int, bool)) ([]int, error) {
	sc := bufio.NewScanner(in)
	visible := make([]int, len(items))
	for i := range items {
		visible[i] = i
	}
	for {
		_, _ = fmt.Fprintf(out, "\n%s\n", title)
		for n, idx := range visible {
			it := items[idx]
			if it.Detail != "" {
				_, _ = fmt.Fprintf(out, "  %3d) %s  (%s)\n", n+1, it.Label, it.Detail)
			} else {
				_, _ = fmt.Fprintf(out, "  %3d) %s\n", n+1, it.Label)
			}
		}
		_, _ = fmt.Fprintf(out, "Choice%s (a number, or text to filter): ", def)
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				return nil, err
			}
			return nil, io.EOF
		}
		answer := strings.TrimSpace(sc.Text())
		if got, ok := pick(answer, visible); ok {
			return got, nil
		}
		// Treat the answer as a filter over the full list.
		var filtered []int
		for i, it := range items {
			if strings.Contains(strings.ToLower(it.Label), strings.ToLower(answer)) {
				filtered = append(filtered, i)
			}
		}
		if len(filtered) == 0 {
			_, _ = fmt.Fprintf(out, "nothing matches %q — showing everything\n", answer)
			filtered = make([]int, len(items))
			for i := range items {
				filtered[i] = i
			}
		}
		visible = filtered
	}
}

// Pick lets the person choose exactly one item and returns its index.
// def is the default index (-1 for none).
func Pick(in io.Reader, out io.Writer, title string, items []Item, def int) (int, error) {
	defHint := ""
	if def >= 0 {
		defHint = fmt.Sprintf(" [%d]", def+1)
	}
	got, err := prompt(in, out, title, items, defHint,
		func(answer string, visible []int) ([]int, bool) {
			if answer == "" && def >= 0 {
				return []int{def}, true
			}
			if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(visible) {
				return []int{visible[n-1]}, true
			}
			return nil, false
		})
	if err != nil {
		return 0, err
	}
	return got[0], nil
}

// PickMulti lets the person choose several items: "3", "1,4", "2-5", "all",
// or combinations ("1,3-5"). Returns original indices in list order.
func PickMulti(in io.Reader, out io.Writer, title string, items []Item) ([]int, error) {
	return prompt(in, out, title+" (e.g. 1,3-5 or all)", items, "",
		func(answer string, visible []int) ([]int, bool) {
			if strings.EqualFold(answer, "all") {
				return append([]int(nil), visible...), true
			}
			got, ok := parseRanges(answer, len(visible))
			if !ok {
				return nil, false
			}
			out := make([]int, 0, len(got))
			for _, n := range got {
				out = append(out, visible[n-1])
			}
			return out, true
		})
}

// parseRanges parses "1,3-5" into sorted unique 1-based numbers within max.
func parseRanges(s string, max int) ([]int, bool) {
	if strings.TrimSpace(s) == "" {
		return nil, false
	}
	seen := map[int]bool{}
	var out []int
	add := func(n int) bool {
		if n < 1 || n > max {
			return false
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
		return true
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		lo, hi, found := strings.Cut(part, "-")
		a, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			return nil, false
		}
		b := a
		if found {
			if b, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				return nil, false
			}
		}
		if b < a {
			a, b = b, a
		}
		for n := a; n <= b; n++ {
			if !add(n) {
				return nil, false
			}
		}
	}
	return out, true
}
