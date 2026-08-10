// Package testapi is an in-process fake of the webtor API used by the CLI's
// testscript suite. One handler serves both error dialects: Mode "webui"
// answers with the {"error":{code,message}} envelope, requires a Bearer key
// and offers the account surface incl. the device flow; Mode "restapi"
// answers with rest-api's bare-string errors and only the resource surface.
package testapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

const (
	SintelID  = "08ada5a7a6183aae1e09d831df6748d566095a10"
	ValidKey  = "99999999-8888-7777-6666-555555555555"
	FileBytes = "0123456789abcdef0123456789abcdef" // /dl payload
)

type Server struct {
	Mode string // "webui" or "restapi"
	// ConfirmAfter is how many device/token polls answer pending before the
	// key is delivered.
	ConfirmAfter int

	mu      sync.Mutex
	polls   int
	library map[string]bool
	pledged map[string]int // resource -> status polls served
}

func New(mode string) *Server {
	return &Server{Mode: mode, ConfirmAfter: 1, library: map[string]bool{}, pledged: map[string]int{}}
}

func (s *Server) fail(w http.ResponseWriter, status int, code, msg string) {
	w.WriteHeader(status)
	if s.Mode == "webui" {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if s.Mode == "webui" {
		if p == "/device/code" || p == "/device/token" {
			s.device(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+ValidKey {
			s.fail(w, 401, "unauthorized", "missing or invalid API key")
			return
		}
	}
	switch {
	case r.Method == "POST" && (p == "/resource/" || p == "/resource"):
		s.addResource(w, r)
	case r.Method == "GET" && p == "/resource/"+SintelID:
		s.writeResource(w)
	case r.Method == "GET" && p == "/resource/"+SintelID+"/list":
		s.list(w, r)
	case r.Method == "GET" && strings.HasPrefix(p, "/resource/"+SintelID+"/export/"):
		s.export(w, r, strings.TrimPrefix(p, "/resource/"+SintelID+"/export/"))
	case strings.HasPrefix(p, "/dl/"):
		s.download(w, r)
	case s.Mode == "webui" && strings.HasPrefix(p, "/library"):
		s.libraryHandler(w, r)
	case s.Mode == "webui" && strings.HasPrefix(p, "/vault"):
		s.vault(w, r)
	case s.Mode == "webui" && p == "/profile":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user_id": "3fa85f64-5717-4562-b3fc-2c963f66afa6", "email": "user@example.com",
			"tier": map[string]any{"id": 1, "name": "Pro"},
			"settings": map[string]any{"show_adult": false},
			"scopes":   []string{"api:read", "api:write"},
		})
	default:
		s.fail(w, 404, "not_found", "resource not found")
	}
}

func (s *Server) writeResource(w http.ResponseWriter) {
	_, _ = w.Write([]byte(`{"id":"` + SintelID + `","name":"Sintel","magnet_uri":"magnet:?xt=urn:btih:` + SintelID + `","multi_file":true,"size":734003200,"files_count":2}`))
}

func (s *Server) addResource(w http.ResponseWriter, r *http.Request) {
	b := make([]byte, 64)
	n, _ := r.Body.Read(b)
	if n == 0 {
		s.fail(w, 400, "bad_request", "empty body")
		return
	}
	s.writeResource(w)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("limit") == "" {
		// The SDK promises an explicit limit on every call; a missing one is
		// a regression worth failing loudly in scripts.
		s.fail(w, 400, "bad_request", "test server requires an explicit limit")
		return
	}
	_, _ = w.Write([]byte(`{"id":"root","path":"/","type":"directory","size":734003200,"items":[
		{"id":"aaa","name":"Sintel.mkv","path":"/Sintel/Sintel.mkv","type":"file","size":734003000,"media_format":"video","mime_type":"video/x-matroska","ext":"mkv","index":0},
		{"id":"bbb","name":"poster.jpg","path":"/Sintel/poster.jpg","type":"file","size":200,"media_format":"image","index":1}],"items_count":2}`))
}

func (s *Server) export(w http.ResponseWriter, r *http.Request, cid string) {
	if cid != "aaa" && cid != "0" && cid != "root" {
		s.fail(w, 404, "not_found", "content not found")
		return
	}
	base := "http://" + r.Host
	exports := map[string]any{
		"download": map[string]any{"url": base + "/dl/" + cid, "meta": map[string]any{"cache": true}},
	}
	if q := r.URL.Query().Get("types"); q == "" || strings.Contains(q, "stream") {
		exports["stream"] = map[string]any{"url": base + "/hls/index.m3u8"}
	}
	src := map[string]any{"id": "aaa", "name": "Sintel.mkv", "path": "/Sintel/Sintel.mkv",
		"type": "file", "size": len(FileBytes)}
	if cid == "root" {
		src = map[string]any{"id": "root", "name": "Sintel", "path": "/", "type": "directory", "size": len(FileBytes)}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"source": src, "exports": exports})
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	body := []byte(FileBytes)
	if rng := r.Header.Get("Range"); rng != "" {
		var start int
		_, _ = fmt.Sscanf(rng, "bytes=%d-", &start)
		w.Header().Set("Content-Range",
			fmt.Sprintf("bytes %d-%d/%d", start, len(body)-1, len(body)))
		w.WriteHeader(206)
		body = body[start:]
	}
	_, _ = w.Write(body)
}

func (s *Server) device(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/device/code":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code": "6c0b8bad-4b41-4bcb-9d10-4c0a0a8e1e3f", "user_code": "F7KQ-29XD",
			"verification_uri":          "http://" + r.Host + "/device",
			"verification_uri_complete": "http://" + r.Host + "/device?code=F7KQ-29XD",
			"expires_in":                600, "interval": 0,
		})
	case "/device/token":
		s.mu.Lock()
		s.polls++
		done := s.polls > s.ConfirmAfter
		s.mu.Unlock()
		if !done {
			s.fail(w, 400, "authorization_pending", "waiting for confirmation")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"key": ValidKey})
	}
}

func (s *Server) libraryHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := map[string]any{"resource_id": SintelID, "name": "Sintel",
		"size": 734003200, "files_count": 2, "added_at": "2026-01-02T15:04:05Z"}
	switch {
	case r.Method == "GET" && r.URL.Path == "/library":
		items := []any{}
		if s.library[SintelID] {
			items = append(items, item)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "items_count": len(items),
			"limit": 100, "offset": 0, "type": "all", "sort": "recent"})
	case r.Method == "POST" && r.URL.Path == "/library":
		if s.library[SintelID] {
			_ = json.NewEncoder(w).Encode(item) // 200: idempotent re-add
			return
		}
		s.library[SintelID] = true
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(item)
	case r.Method == "DELETE" && r.URL.Path == "/library/"+SintelID:
		if !s.library[SintelID] {
			s.fail(w, 404, "not_found", "not in library")
			return
		}
		delete(s.library, SintelID)
		w.WriteHeader(204)
	case r.Method == "PATCH" && r.URL.Path == "/library/"+SintelID:
		var req struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		item["name"] = req.Name
		_ = json.NewEncoder(w).Encode(item)
	case r.Method == "GET" && r.URL.Path == "/library/"+SintelID:
		if !s.library[SintelID] {
			s.fail(w, 404, "not_found", "not in library")
			return
		}
		_ = json.NewEncoder(w).Encode(item)
	default:
		s.fail(w, 404, "not_found", "resource not found")
	}
}

func (s *Server) vault(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pledge := map[string]any{"pledge_id": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
		"resource_id": SintelID, "name": "Sintel", "amount": 1, "frozen": false,
		"funded": true, "vaulted": false, "expired": false, "required_vp": 1,
		"funded_vp": 1, "created_at": "2026-01-02T15:04:05Z"}
	switch {
	case r.Method == "GET" && r.URL.Path == "/vault":
		total, avail := 100.0, 99.0
		_ = json.NewEncoder(w).Encode(map[string]any{
			"points":  map[string]any{"total": total, "available": avail, "funded": 1, "frozen": 0, "claimable": 1},
			"content": map[string]any{"vaulted": 0, "loading": 1, "expiring": 0},
			"pledges": []any{pledge},
		})
	case r.Method == "POST" && r.URL.Path == "/vault/pledges":
		if _, ok := s.pledged[SintelID]; ok {
			s.fail(w, 409, "conflict", "pledge already exists")
			return
		}
		s.pledged[SintelID] = 0
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(pledge)
	case r.Method == "GET" && r.URL.Path == "/vault/pledges/"+SintelID:
		n := s.pledged[SintelID]
		s.pledged[SintelID] = n + 1
		st := map[string]any{}
		for k, v := range pledge {
			st[k] = v
		}
		switch {
		case n == 0:
			st["status"] = "storing"
			st["progress"] = 50.0
			st["stored_size"] = 367001600
			st["total_size"] = 734003200
		default:
			st["status"] = "vaulted"
			st["vaulted"] = true
		}
		_ = json.NewEncoder(w).Encode(st)
	case r.Method == "DELETE" && r.URL.Path == "/vault/pledges/"+SintelID:
		delete(s.pledged, SintelID)
		w.WriteHeader(204)
	default:
		s.fail(w, 404, "not_found", "resource not found")
	}
}
