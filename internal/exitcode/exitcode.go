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
	Private     = 8 // private torrent this account cannot use
)

// Hints appended to the server's text for the two private_torrent reasons the
// user can act on from the CLI.
const (
	hintSignIn   = "run `webtor auth login`"
	hintNoRecord = "add your .torrent: `webtor add file.torrent`"
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
	if err.Error() == "" {
		// A command that already reported its own failure line by line
		// (doctor) returns an empty error: exit non-zero, print nothing.
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
		if code, msg, ok := classifyPrivate(ae); ok {
			return code, msg
		}
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

// classifyPrivate maps a private torrent refusal onto its exit code; ok is
// false for any other code. The message is the server's text as is, with a
// hint only for private_torrent's sign_in and no_record reasons.
func classifyPrivate(ae *webtor.Error) (exit int, msg string, ok bool) {
	msg = ae.Message
	if msg == "" {
		msg = ae.Error()
	}
	switch ae.Code {
	case webtor.CodePrivateTorrent:
		switch ae.Reason {
		case webtor.ReasonSignIn:
			return Auth, msg + " — " + hintSignIn, true
		case webtor.ReasonNoRecord:
			return Private, msg + " — " + hintNoRecord, true
		}
		return Private, msg, true
	case webtor.CodePrivateNeedsAccount:
		return Auth, msg, true
	case webtor.CodePrivateNoTracker, webtor.CodePrivateTooManyTrackers, webtor.CodePrivateMagnet,
		webtor.CodePrivateTrackerRefused, webtor.CodePrivateTorrentFile:
		return Private, msg, true
	case webtor.CodePrivateVerifyUnavailable:
		return Throttled, msg, true
	case webtor.CodePrivateUnavailable:
		return Unsupported, msg, true
	}
	return 0, "", false
}
