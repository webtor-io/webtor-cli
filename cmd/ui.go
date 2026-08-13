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
