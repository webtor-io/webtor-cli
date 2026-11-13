package cmd

import (
	"testing"

	webtor "github.com/webtor-io/api-sdk-go"
)

func TestNeedsAdd(t *testing.T) {
	const h = "0123456789abcdef0123456789abcdef01234567"
	withTr := "magnet:?xt=urn:btih:" + h + "&tr=http%3A%2F%2Ftracker.example%2Fannounce"
	bare := "magnet:?xt=urn:btih:" + h
	noRecord := &webtor.Error{HTTPStatus: 403, Code: webtor.CodePrivateTorrent, Reason: webtor.ReasonNoRecord}
	notFound := &webtor.Error{HTTPStatus: 404, Code: webtor.CodeNotFound}
	priv, pub := &webtor.ResourceResponse{ID: h, Private: true}, &webtor.ResourceResponse{ID: h}
	for _, tc := range []struct {
		name string
		arg  string
		res  *webtor.ResourceResponse
		err  error
		want bool
	}{
		{"not found magnet", bare, nil, notFound, true},
		{"not found hash", h, nil, notFound, false},
		{"enforce: refused, own tr", withTr, nil, noRecord, true},
		{"enforce: refused, bare", bare, nil, noRecord, false},
		{"observe: private 200, own tr", withTr, priv, nil, true},
		{"public 200 with tr", withTr, pub, nil, false},
		{"empty tr", bare + "&tr=", priv, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsAdd(tc.arg, tc.res, tc.err); got != tc.want {
				t.Errorf("needsAdd = %v, want %v", got, tc.want)
			}
		})
	}
}
