package brain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func vault(t *testing.T) *Vault {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"wiki/daily", "raw/inbox", "tools"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	os.WriteFile(filepath.Join(root, "wiki", "board.md"), []byte("# Board\n"), 0o644)
	os.WriteFile(filepath.Join(root, "wiki", "daily", "2026-11-06.md"), []byte("# 2026-11-06\n"), 0o644)
	os.WriteFile(filepath.Join(root, "secret.md"), []byte("outside wiki"), 0o644)
	now := time.Date(2026, 11, 6, 9, 30, 0, 0, time.UTC)
	return &Vault{Root: root, Now: func() time.Time { return now }}
}

func TestPageReadsOnlyInsideWiki(t *testing.T) {
	v := vault(t)
	if s, err := v.Page("board"); err != nil || s != "# Board\n" {
		t.Fatalf("board: %q %v", s, err)
	}
	if s, err := v.Today(); err != nil || !strings.Contains(s, "2026-11-06") {
		t.Fatalf("today: %q %v", s, err)
	}
	for _, bad := range []string{"../secret", "..", "/etc/passwd", "daily/../../secret", "Board", "a b", "", "board.md/x", "-x"} {
		if _, err := v.Page(bad); !errors.Is(err, ErrBadName) {
			t.Errorf("Page(%q): err = %v, want ErrBadName", bad, err)
		}
	}
	if _, err := v.Page("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing page: %v", err)
	}
	// a symlink inside wiki/ that points outside it must not be followed
	os.Symlink(filepath.Join(v.Root, "secret.md"), filepath.Join(v.Root, "wiki", "escape.md"))
	if _, err := v.Page("escape"); !errors.Is(err, ErrBadName) {
		t.Errorf("symlink escape: err = %v, want ErrBadName", err)
	}
}

func TestSaveWritesNewFilesOnly(t *testing.T) {
	v := vault(t)
	p1, err := v.Save(Capture{Kind: "text", Text: "Call Sofia about the Lisbon leads!", From: "ilya@github"})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := v.Save(Capture{Kind: "text", Text: "Call Sofia about the Lisbon leads!"})
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatal("a second capture overwrote the first")
	}
	if !strings.HasPrefix(p1, "raw/inbox/2026-11-06_0930_tether-text-call-sofia-about-the-lisbon-leads") {
		t.Fatalf("path %q", p1)
	}
	b, _ := os.ReadFile(filepath.Join(v.Root, p1))
	for _, want := range []string{"source: tether", "kind: text", `from: "ilya@github"`, "Call Sofia about the Lisbon leads!"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("note lacks %q:\n%s", want, b)
		}
	}
}

func TestSavePhotoKeepsOnlyASafeExtension(t *testing.T) {
	v := vault(t)
	p, err := v.Save(Capture{Kind: "photo", Text: "whiteboard", Photo: strings.NewReader("jpegbytes"), PhotoName: "../../evil.sh"})
	if err != nil {
		t.Fatal(err)
	}
	note, _ := os.ReadFile(filepath.Join(v.Root, p))
	if !strings.Contains(string(note), "![photo](2026-11-06_0930_tether-photo-whiteboard.bin)") {
		t.Fatalf("note:\n%s", note)
	}
	entries, _ := os.ReadDir(filepath.Join(v.Root, "raw", "inbox"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sh") {
			t.Fatalf("client-chosen extension kept: %s", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(v.Root, "..", "evil.sh")); err == nil {
		t.Fatal("photo written outside the inbox")
	}
}

func TestSaveRejectsOversizedPhotoAndLeavesNothing(t *testing.T) {
	v := vault(t)
	big := strings.NewReader(strings.Repeat("x", MaxPhoto+10))
	if _, err := v.Save(Capture{Kind: "photo", Photo: big, PhotoName: "a.jpg"}); err == nil {
		t.Fatal("oversized photo accepted")
	}
	if entries, _ := os.ReadDir(filepath.Join(v.Root, "raw", "inbox")); len(entries) != 0 {
		t.Fatalf("left %d files behind", len(entries))
	}
}

func TestSaveRejectsUnknownKindAndEmpty(t *testing.T) {
	v := vault(t)
	if _, err := v.Save(Capture{Kind: "exec", Text: "x"}); err == nil {
		t.Error("unknown kind accepted")
	}
	if _, err := v.Save(Capture{Kind: "text", Text: "   "}); err == nil {
		t.Error("empty capture accepted")
	}
}

func TestSearchPassesQueryAsArgument(t *testing.T) {
	v := vault(t)
	// a fake search tool that echoes its arguments
	os.WriteFile(filepath.Join(v.Root, "tools", "search.py"), []byte("import sys\nprint('|'.join(sys.argv[1:]))\n"), 0o644)
	out, err := v.Search(context.Background(), `-rf; $(touch pwned) "x"`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != `-n|8|--|-rf; $(touch pwned) "x"` {
		t.Fatalf("argv = %q", out)
	}
	if _, err := os.Stat(filepath.Join(v.Root, "pwned")); err == nil {
		t.Fatal("the query was run by a shell")
	}
	if _, err := v.Search(context.Background(), strings.Repeat("a", 201)); err == nil {
		t.Error("overlong query accepted")
	}
}
