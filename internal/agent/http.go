package agent

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

//go:embed web.html
var web []byte

//go:embed app.js
var appJS []byte

type Server struct {
	Runtime             *Runtime
	Store               *Store
	APIKey, ApprovalKey string
	slots               chan struct{}
	Campus              *CampusClient
}

func NewServer(r *Runtime, s *Store, key, approval string) *Server {
	return &Server{Runtime: r, Store: s, APIKey: key, ApprovalKey: approval, slots: make(chan struct{}, 2)}
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func authorized(r *http.Request, key string) bool {
	v := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return len(key) >= 16 && subtle.ConstantTimeCompare([]byte(v), []byte(key)) == 1 && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
}
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(appJS)
	})
	m.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(web)
	})
	m.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if s.Store.db.PingContext(r.Context()) != nil {
			write(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		write(w, 200, map[string]string{"status": "ok", "mode": s.Runtime.Model.Name()})
	})
	m.HandleFunc("POST /api/runs", func(w http.ResponseWriter, r *http.Request) {
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		default:
			write(w, 429, map[string]string{"error": "two runs already active"})
			return
		}
		var input struct {
			Question string `json:"question"`
			DrawID   int64  `json:"draw_id,omitempty"`
		}
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
		if e != nil || Strict(b, &input) != nil || len(input.Question) < 2 || len(input.Question) > 4000 {
			write(w, 400, map[string]string{"error": "question must be 2-4000 bytes; strict JSON required"})
			return
		}
		id := make([]byte, 16)
		if _, e = rand.Read(id); e != nil {
			write(w, 500, map[string]string{"error": "cannot create ID"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		var live *LiveSnapshot
		if input.DrawID < 0 {
			write(w, 400, map[string]string{"error": "draw_id must be positive or omitted"})
			return
		}
		if input.DrawID > 0 {
			if s.Campus == nil {
				write(w, 503, map[string]string{"error": "Campus integration not configured"})
				return
			}
			v, e := s.Campus.Inspect(ctx, input.DrawID)
			if e != nil {
				write(w, 502, map[string]string{"error": e.Error()})
				return
			}
			live = &v
		}
		result := s.Runtime.run(ctx, hex.EncodeToString(id), input.Question, live)
		// Store terminal traces even if the browser disconnected; no secret model headers are saved.
		saveCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if e = s.Store.Save(saveCtx, input.Question, result); e != nil {
			write(w, 500, map[string]string{"error": "could not persist run"})
			return
		}
		write(w, 200, result)
	})
	m.HandleFunc("GET /api/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Store.Get(r.Context(), r.PathValue("id"))
		if errors.Is(e, sql.ErrNoRows) {
			write(w, 404, map[string]string{"error": "not found"})
			return
		}
		if e != nil {
			write(w, 500, map[string]string{"error": "database error"})
			return
		}
		write(w, 200, v)
	})
	m.HandleFunc("POST /api/runs/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Confirm bool `json:"confirm"`
		}
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
		if e != nil || Strict(b, &input) != nil || !input.Confirm {
			write(w, 400, map[string]string{"error": "explicit confirm=true required"})
			return
		}
		t, e := s.Store.Approve(r.Context(), r.PathValue("id"))
		if errors.Is(e, sql.ErrNoRows) {
			write(w, 404, map[string]string{"error": "not found"})
			return
		}
		if errors.Is(e, ErrNoDraft) {
			write(w, 409, map[string]string{"error": e.Error()})
			return
		}
		if e != nil {
			write(w, 500, map[string]string{"error": "database error"})
			return
		}
		write(w, 200, t)
	})
	m.HandleFunc("POST /api/runs/{id}/replay/approve", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Confirm bool `json:"confirm"`
		}
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
		if e != nil || Strict(b, &in) != nil || !in.Confirm {
			write(w, 400, map[string]string{"error": "confirm=true required"})
			return
		}
		v, e := s.ApproveReplay(r.Context(), r.PathValue("id"))
		if e != nil {
			status := 409
			var upstream UpstreamError
			if errors.As(e, &upstream) && upstream.Status >= 500 {
				status = 502
			}
			write(w, status, map[string]string{"error": e.Error()})
			return
		}
		write(w, 200, v)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			key := s.APIKey
			if strings.HasSuffix(r.URL.Path, "/approve") {
				key = s.ApprovalKey
			}
			if !authorized(r, key) {
				write(w, 401, map[string]string{"error": "invalid bearer key"})
				return
			}
		}
		m.ServeHTTP(w, r)
	})
}
