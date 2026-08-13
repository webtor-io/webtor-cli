// Package exitcode maps SDK errors onto the CLI's exit codes and friendly
// messages, so scripts can branch on the class of failure.
package exitcode

import (
	"context"
	"errors"
	"fmt"

	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/picker"
)

// Exit codes. 0 is success.
const (
	OK          = 0
	Err         = 1 // any other error
	Usage       = 2 // bad invocation or missing configuration
	Auth        = 3 // unauthorized / forbidden
	Payment     = 4 // free tier hitting the paid-only API
	NotFound    = 5
	Unsupported = 6 // capability missing on this backend
	Throttled   = 7 // rate-limited or upstream timeout after retries
)

// UsageError marks an invocation error (exit code 2).
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Usagef builds a UsageError.
func Usagef(format string, a ...any) error {
	return &UsageError{Msg: fmt.Sprintf(format, a...)}
}

// Classify returns the exit code and the message to print for err.
func Classify(err error) (int, string) {
	if err == nil {
		return OK, ""
	}
	if errors.Is(err, picker.ErrCancelled) || errors.Is(err, picker.ErrBack) || errors.Is(err, picker.ErrTab) {
		// Backing out of a picker is not an error worth narrating. ErrBack
		// reaches here only from screens with no parent to return to.
		return Err, ""
	}
	var ue *UsageError
	if errors.As(err, &ue) {
		return Usage, ue.Msg
	}
	var ce *webtor.CapabilityError
	if errors.As(err, &ce) {
		return Unsupported, ce.Error() + " — switch to a webtor.io account context (webtor config init)"
	}
	var ae *webtor.Error
	if errors.As(err, &ae) {
		switch ae.Code {
		case webtor.CodeUnauthorized, webtor.CodeForbidden:
			return Auth, err.Error() + "\nrun `webtor auth login` to (re)authenticate"
		case webtor.CodePaymentRequired:
			return Payment, "the webtor.io API is available on paid plans — upgrade at https://webtor.io/donate"
		case webtor.CodeNotFound:
			return NotFound, err.Error()
		case webtor.CodeRateLimited, webtor.CodeUpstreamTimeout:
			return Throttled, err.Error()
		}
		return Err, err.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Throttled, err.Error()
	}
	return Err, err.Error()
}
