package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kage7047/tether/internal/brain"
)

func setup(t *testing.T) (*brain.Vault, http.Handler) {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "wiki", "daily"), 0o755)
	os.WriteFile(filepath.Join(root, "wiki", "board.md"), []byte("# Board"), 0o644)
	v := &brain.Vault{Root: root, Now: func() time.Time { return time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC) }}
	s := &Server{
		Vault: v,
		// tests name the user in a header; in production the tailnet's WhoIs does
		Identify: func(r *http.Request) (string, error) { return r.Header.Get("Test-Login"), nil },
		Allowed:  []string{"ilya@github"},
		Log:      slog.New(slog.DiscardHandler),
	}
	return v, s.Handler()
}

func do(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestOnlyAllowedLoginsGetIn(t *testing.T) {
	_, h := setup(t)
	for login, want := range map[string]int{"": 403, "eve@github": 403, "ilya@github": 200} {
		r := httptest.NewRequest("GET", "/api/page?name=board", nil)
		r.Header.Set("Test-Login", login)
		if got := do(h, r).Code; got != want {
			t.Errorf("login %q: %d, want %d", login, got, want)
		}
	}
}

func form(t *testing.T, fields map[string]string, photo string) (*bytes.Buffer, string) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	if photo != "" {
		fw, _ := mw.CreateFormFile("photo", photo)
		fw.Write([]byte("fake image"))
	}
	mw.Close()
	return &b, mw.FormDataContentType()
}

func TestCaptureNeedsSameOriginWrite(t *testing.T) {
	v, h := setup(t)
	post := func(header, origin string) int {
		body, ct := form(t, map[string]string{"kind": "text", "text": "buy milk"}, "")
		r := httptest.NewRequest("POST", "http://tether.tail/api/capture", body)
		r.Host = "tether.tail"
		r.Header.Set("Content-Type", ct)
		r.Header.Set("Test-Login", "ilya@github")
		if header != "" {
			r.Header.Set("X-Tether", header)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return do(h, r).Code
	}
	if c := post("", ""); c != 403 {
		t.Errorf("no custom header: %d", c)
	}
	if c := post("1", "https://evil.example"); c != 403 {
		t.Errorf("foreign origin: %d", c)
	}
	if c := post("1", "https://tether.tail"); c != 200 {
		t.Errorf("same origin: %d", c)
	}
	entries, _ := os.ReadDir(filepath.Join(v.Root, "raw", "inbox"))
	if len(entries) != 1 {
		t.Fatalf("%d notes written, want exactly 1", len(entries))
	}
	note, _ := os.ReadFile(filepath.Join(v.Root, "raw", "inbox", entries[0].Name()))
	if !strings.Contains(string(note), `from: "ilya@github"`) {
		t.Errorf("note does not record who sent it:\n%s", note)
	}
}

func TestPhotoCapture(t *testing.T) {
	v, h := setup(t)
	body, ct := form(t, map[string]string{"text": "whiteboard"}, "IMG_0001.HEIC")
	r := httptest.NewRequest("POST", "/api/capture", body)
	r.Header.Set("Content-Type", ct)
	r.Header.Set("Test-Login", "ilya@github")
	r.Header.Set("X-Tether", "1")
	w := do(h, r)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var res map[string]string
	json.Unmarshal(w.Body.Bytes(), &res)
	if _, err := os.Stat(filepath.Join(v.Root, strings.TrimSuffix(res["path"], ".md")+".heic")); err != nil {
		t.Fatalf("photo not stored next to %s: %v", res["path"], err)
	}
}

func TestPageErrors(t *testing.T) {
	_, h := setup(t)
	for name, want := range map[string]int{"board": 200, "nope": 404, "../../etc/passwd": 400} {
		r := httptest.NewRequest("GET", "/api/page?name="+name, nil)
		r.Header.Set("Test-Login", "ilya@github")
		if got := do(h, r).Code; got != want {
			t.Errorf("page %q: %d, want %d", name, got, want)
		}
	}
}
