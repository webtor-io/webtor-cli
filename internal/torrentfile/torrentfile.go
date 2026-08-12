// Package torrentfile reads the little a scanner needs from .torrent files:
// name, total size, file count and the infohash. It carries its own minimal
// bencode walker instead of a BitTorrent dependency — the infohash is the
// SHA-1 of the raw bytes of the top-level "info" value, so the walker tracks
// byte ranges while decoding.
package torrentfile

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
)

// Info is what Parse extracts from a .torrent file.
type Info struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	InfoHash   string `json:"infohash"`
	Size       int64  `json:"size"`
	FilesCount int    `json:"files_count"`
}

// Parse reads and parses one .torrent file.
func Parse(path string) (*Info, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseBytes(path, b)
}

// ParseBytes parses .torrent content.
func ParseBytes(path string, b []byte) (*Info, error) {
	d := &decoder{b: b}
	top, err := d.value()
	if err != nil {
		return nil, fmt.Errorf("%s: not a torrent file: %w", path, err)
	}
	dict, ok := top.(map[string]item)
	if !ok {
		return nil, fmt.Errorf("%s: not a torrent file: top level is not a dict", path)
	}
	info, ok := dict["info"]
	if !ok {
		return nil, fmt.Errorf("%s: not a torrent file: no info dict", path)
	}
	infoDict, ok := info.v.(map[string]item)
	if !ok {
		return nil, fmt.Errorf("%s: malformed torrent: info is not a dict", path)
	}
	sum := sha1.Sum(b[info.start:info.end])

	out := &Info{Path: path, InfoHash: hex.EncodeToString(sum[:])}
	if n, ok := infoDict["name"]; ok {
		if s, ok := n.v.(string); ok {
			out.Name = s
		}
	}
	switch {
	case has(infoDict, "files"): // multi-file mode
		files, ok := infoDict["files"].v.([]item)
		if !ok {
			return nil, fmt.Errorf("%s: malformed torrent: files is not a list", path)
		}
		for _, f := range files {
			fd, ok := f.v.(map[string]item)
			if !ok {
				continue
			}
			if l, ok := fd["length"]; ok {
				if n, ok := l.v.(int64); ok {
					out.Size += n
					out.FilesCount++
				}
			}
		}
	case has(infoDict, "length"): // single-file mode
		if n, ok := infoDict["length"].v.(int64); ok {
			out.Size = n
			out.FilesCount = 1
		}
	}
	return out, nil
}

func has(d map[string]item, k string) bool { _, ok := d[k]; return ok }

// item is a decoded bencode value plus the byte range it occupied.
type item struct {
	v          any
	start, end int
}

type decoder struct {
	b []byte
	i int
}

func (d *decoder) value() (any, error) {
	it, err := d.item()
	if err != nil {
		return nil, err
	}
	return it.v, nil
}

func (d *decoder) item() (item, error) {
	start := d.i
	if d.i >= len(d.b) {
		return item{}, fmt.Errorf("truncated at %d", d.i)
	}
	switch c := d.b[d.i]; {
	case c == 'i':
		d.i++
		n, err := d.number('e')
		if err != nil {
			return item{}, err
		}
		return item{v: n, start: start, end: d.i}, nil
	case c >= '0' && c <= '9':
		n, err := d.number(':')
		if err != nil {
			return item{}, err
		}
		if n < 0 || d.i+int(n) > len(d.b) {
			return item{}, fmt.Errorf("string overruns buffer at %d", start)
		}
		s := string(d.b[d.i : d.i+int(n)])
		d.i += int(n)
		return item{v: s, start: start, end: d.i}, nil
	case c == 'l':
		d.i++
		var list []item
		for {
			if d.i >= len(d.b) {
				return item{}, fmt.Errorf("unterminated list at %d", start)
			}
			if d.b[d.i] == 'e' {
				d.i++
				return item{v: list, start: start, end: d.i}, nil
			}
			it, err := d.item()
			if err != nil {
				return item{}, err
			}
			list = append(list, it)
		}
	case c == 'd':
		d.i++
		dict := map[string]item{}
		for {
			if d.i >= len(d.b) {
				return item{}, fmt.Errorf("unterminated dict at %d", start)
			}
			if d.b[d.i] == 'e' {
				d.i++
				return item{v: dict, start: start, end: d.i}, nil
			}
			key, err := d.item()
			if err != nil {
				return item{}, err
			}
			ks, ok := key.v.(string)
			if !ok {
				return item{}, fmt.Errorf("non-string dict key at %d", key.start)
			}
			val, err := d.item()
			if err != nil {
				return item{}, err
			}
			dict[ks] = val
		}
	default:
		return item{}, fmt.Errorf("unexpected byte %q at %d", c, d.i)
	}
}

// number reads digits (with an optional leading minus) up to term.
func (d *decoder) number(term byte) (int64, error) {
	var n int64
	neg := false
	digits := 0
	for d.i < len(d.b) {
		c := d.b[d.i]
		switch {
		case c == term && digits > 0:
			d.i++
			if neg {
				n = -n
			}
			return n, nil
		case c == '-' && digits == 0 && !neg:
			neg = true
		case c >= '0' && c <= '9':
			n = n*10 + int64(c-'0')
			digits++
		default:
			return 0, fmt.Errorf("bad number at %d", d.i)
		}
		d.i++
	}
	return 0, fmt.Errorf("unterminated number")
}
