package exitcode

import (
	"fmt"
	"strings"
	"testing"

	webtor "github.com/webtor-io/api-sdk-go"
)

func pe(status int, code, reason string) error {
	return &webtor.Error{HTTPStatus: status, Code: code, Reason: reason, Message: "refused by server"}
}

func TestClassifyPrivate(t *testing.T) {
	if Private != 8 {
		t.Fatalf("Private = %d; exit code 8 is a contract", Private)
	}
	const srv = "refused by server" // pe() puts it into Message: the server's text is printed as is
	for _, tc := range []struct {
		name         string
		err          error
		want         int
		wantMsg, not string
	}{
		{"sign in", pe(403, webtor.CodePrivateTorrent, webtor.ReasonSignIn), Auth, srv + " — run `webtor auth login`", "webtor add"},
		{"no record", pe(403, webtor.CodePrivateTorrent, webtor.ReasonNoRecord), Private, srv + " — add your .torrent: `webtor add file.torrent`", "auth login"},
		{"no reason", pe(403, webtor.CodePrivateTorrent, ""), Private, srv, "webtor a"},
		{"other reason", pe(403, webtor.CodePrivateTorrent, "token_in_query"), Private, srv, "webtor a"},
		{"needs account", pe(422, webtor.CodePrivateNeedsAccount, ""), Auth, srv, "webtor a"},
		{"no tracker", pe(422, webtor.CodePrivateNoTracker, ""), Private, srv, "webtor a"},
		{"too many", pe(422, webtor.CodePrivateTooManyTrackers, ""), Private, srv, "webtor a"},
		{"bare magnet", pe(422, webtor.CodePrivateMagnet, ""), Private, srv, "webtor a"},
		{"refused", pe(422, webtor.CodePrivateTrackerRefused, "key"), Private, srv, "webtor a"},
		{"verify unavailable", pe(503, webtor.CodePrivateVerifyUnavailable, ""), Throttled, srv, "webtor a"},
		{"torrent file", pe(403, webtor.CodePrivateTorrentFile, ""), Private, srv, "webtor a"},
		{"unavailable 403", pe(403, webtor.CodePrivateUnavailable, ""), Unsupported, srv, "webtor a"},
		{"unavailable 422", pe(422, webtor.CodePrivateUnavailable, ""), Unsupported, srv, "webtor a"},
		{"wrapped", fmt.Errorf("webtor: resuming download after %q: %w", "unexpected EOF",
			pe(403, webtor.CodePrivateTorrent, webtor.ReasonNoRecord)), Private, "webtor add file.torrent", ""},
		{"empty server text", &webtor.Error{HTTPStatus: 422, Code: webtor.CodePrivateNoTracker}, Private, "private_no_tracker", ""},
		{"plain forbidden unchanged", pe(403, webtor.CodeForbidden, ""), Auth, "auth login", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := Classify(tc.err)
			if got != tc.want || !strings.Contains(msg, tc.wantMsg) || (tc.not != "" && strings.Contains(msg, tc.not)) {
				t.Errorf("Classify = %d %q; want %d containing %q, without %q", got, msg, tc.want, tc.wantMsg, tc.not)
			}
		})
	}
}
