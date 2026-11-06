// Package server is tether's HTTP API and the embedded PWA. Who is asking comes from the
// network, not from a password: on the tailnet, Tailscale's WhoIs names the logged-in user behind
// every connection, and only logins on the allowlist get through.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/Kage7047/tether/internal/brain"
)

// Identify names the user behind a request ("" if unknown).
type Identify func(r *http.Request) (string, error)

// Server serves the API and the app.
type Server struct {
	Vault    *brain.Vault
	Identify Identify
	Allowed  []string // tailnet login names allowed in, e.g. "kage7047@github"
	// Hosts are the names this server answers to (tailnet DNS name, its IPs, or localhost in dev
	// mode). Any other Host header is refused, which stops DNS rebinding: a hostile site pointing
	// its own name at tether's address would otherwise count as same-origin in the browser.
	Hosts []string
	App      fs.FS    // the built PWA
	Log      *slog.Logger
}

// Handler returns the full HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/capture", s.capture)
	mux.HandleFunc("GET /api/today", s.today)
	mux.HandleFunc("GET /api/page", s.page)
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("GET /api/whoami", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"login": login(r)})
	})
	if s.App != nil {
		mux.Handle("GET /", http.FileServerFS(s.App))
	}
	return s.secure(mux)
}

type ctxKey struct{}

func login(r *http.Request) string {
	l, _ := r.Context().Value(ctxKey{}).(string)
	return l
}

// secure checks identity on every request, blocks cross-site writes and sets safe headers.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")

		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if !slices.Contains(s.Hosts, strings.TrimSuffix(strings.ToLower(host), ".")) {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		who, err := s.Identify(r)
		if err != nil || who == "" || !slices.Contains(s.Allowed, who) {
			s.Log.Warn("refused", "remote", r.RemoteAddr, "login", who, "err", err)
			http.Error(w, "not allowed", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			// A custom header cannot be sent cross-origin without a CORS preflight, which we never
			// grant, and the Origin, when present, must be this host.
			if r.Header.Get("X-Tether") != "1" || !sameOrigin(r) {
				http.Error(w, "cross-site request refused", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, who)))
	})
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true // same-origin fetches from older Safari may omit it; the custom header still holds
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

func (s *Server) capture(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, brain.MaxPhoto+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		http.Error(w, "bad form: "+err.Error(), http.StatusBadRequest)
		return
	}
	c := brain.Capture{Kind: r.FormValue("kind"), Text: r.FormValue("text"), From: login(r)}
	if f, hdr, err := r.FormFile("photo"); err == nil {
		defer f.Close()
		c.Photo, c.PhotoName = f, hdr.Filename
		c.Kind = "photo"
	}
	path, err := s.Vault.Save(c)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.Log.Info("captured", "path", path, "login", c.From)
	writeJSON(w, map[string]string{"path": path})
}

func (s *Server) today(w http.ResponseWriter, r *http.Request) {
	md, err := s.Vault.Today()
	s.markdown(w, md, err)
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	md, err := s.Vault.Page(strings.TrimSuffix(r.URL.Query().Get("name"), ".md"))
	s.markdown(w, md, err)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	out, err := s.Vault.Search(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"results": out})
}

func (s *Server) markdown(w http.ResponseWriter, md string, err error) {
	switch {
	case errors.Is(err, brain.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, brain.ErrBadName):
		http.Error(w, "bad page name", http.StatusBadRequest)
	case err != nil:
		s.Log.Error("read", "err", err)
		http.Error(w, "read failed", http.StatusInternalServerError)
	default:
		writeJSON(w, map[string]string{"markdown": md})
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}
