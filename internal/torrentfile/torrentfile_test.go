package torrentfile

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"testing"
)

// buildTorrent assembles bencode around the given info-dict body and returns
// the whole file plus the expected infohash (SHA-1 of the raw info value).
func buildTorrent(infoBody string) (data []byte, infohash string) {
	info := "d" + infoBody + "e"
	sum := sha1.Sum([]byte(info))
	return []byte("d8:announce7:someurl4:info" + info + "e"), hex.EncodeToString(sum[:])
}

func TestParseSingleFile(t *testing.T) {
	pieces := strings.Repeat("x", 20)
	data, hash := buildTorrent("6:lengthi734003200e4:name10:sintel.mkv12:piece lengthi16384e6:pieces20:" + pieces)
	got, err := ParseBytes("t.torrent", data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "sintel.mkv" || got.Size != 734003200 || got.FilesCount != 1 {
		t.Errorf("got %+v", got)
	}
	if got.InfoHash != hash {
		t.Errorf("infohash = %s, want %s", got.InfoHash, hash)
	}
}

func TestParseMultiFile(t *testing.T) {
	data, hash := buildTorrent(
		"5:filesl" +
			"d6:lengthi100e4:pathl3:dir5:a.mkvee" +
			"d6:lengthi50e4:pathl5:b.srtee" +
			"e4:name6:Season12:piece lengthi16384e6:pieces20:" + strings.Repeat("y", 20))
	got, err := ParseBytes("t.torrent", data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Season" || got.Size != 150 || got.FilesCount != 2 {
		t.Errorf("got %+v", got)
	}
	if got.InfoHash != hash {
		t.Errorf("infohash = %s, want %s", got.InfoHash, hash)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, b := range []string{"", "not bencode", "i42e", "d4:spam4:eggse", "d4:infoi1ee", "d4:info"} {
		if _, err := ParseBytes("x", []byte(b)); err == nil {
			t.Errorf("%q accepted", b)
		}
	}
}

// The infohash must be computed over the *raw* info bytes, not a re-encoded
// form — an info dict with keys in non-sorted order must hash as-is.
func TestInfoHashUsesRawBytes(t *testing.T) {
	info := "d4:name1:x6:lengthi5e12:piece lengthi1e6:pieces20:" + strings.Repeat("z", 20) + "e"
	data := []byte("d4:info" + info + "e")
	sum := sha1.Sum([]byte(info))
	got, err := ParseBytes("t", data)
	if err != nil {
		t.Fatal(err)
	}
	if got.InfoHash != hex.EncodeToString(sum[:]) {
		t.Errorf("infohash not over raw bytes")
	}
}
