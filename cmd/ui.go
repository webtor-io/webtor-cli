package cmd

import (
	"errors"

	"github.com/webtor-io/webtor-cli/internal/picker"
)

// pick wraps picker.Pick with the application-wide Tab gesture: Tab opens
// the downloads screen from any single-select list, then the original
// screen re-prompts. Every interactive menu goes through this.
func pick(title string, items []picker.Item, def int) (int, error) {
	for {
		n, err := picker.Pick(title, items, def)
		if errors.Is(err, picker.ErrTab) {
			if derr := downloadsScreen(); derr != nil && !back(derr) {
				return 0, derr
			}
			continue
		}
		return n, err
	}
}

// confirmScreen asks a yes/no question as a proper screen. Enter on the
// default answers "no"; Esc also answers "no".
func confirmScreen(question, yes, no string) (bool, error) {
	n, err := pick(question, []picker.Item{{Label: no}, {Label: yes}}, 0)
	if back(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
