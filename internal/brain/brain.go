// Package brain reads and writes Ilya's second-brain vault: captures go to raw/inbox as new files
// (never edits of existing ones), pages are read from wiki/ by name, and search shells out to the
// vault's own tools/search.py with arguments passed as argv, never through a shell.
package brain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Vault is a second-brain directory.
type Vault struct {
	Root string
	// Now is the clock; tests replace it.
	Now func() time.Time
	// Python runs tools/search.py (default "python3").
	Python string
}

var (
	// ErrNotFound is returned for a page that does not exist.
	ErrNotFound = errors.New("brain: page not found")
	// ErrBadName is returned for a page name that could escape wiki/ or is not a plain note name.
	ErrBadName = errors.New("brain: bad page name")
)

// A page name is a path of plain segments under wiki/, like "board" or "daily/2026-11-06".
var pageName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*(/[a-z0-9][a-z0-9_-]*)*$`)

// Page returns the markdown of wiki/<name>.md.
func (v *Vault) Page(name string) (string, error) {
	if !pageName.MatchString(name) || strings.Contains(name, "..") {
		return "", ErrBadName
	}
	wiki := filepath.Join(v.Root, "wiki")
	p := filepath.Join(wiki, filepath.FromSlash(name)+".md")
	// Resolve symlinks and check the result is still inside wiki/.
	real, err := filepath.EvalSymlinks(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	realWiki, err := filepath.EvalSymlinks(wiki)
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(realWiki, real); err != nil || strings.HasPrefix(rel, "..") {
		return "", ErrBadName
	}
	b, err := os.ReadFile(real)
	return string(b), err
}

// Today returns today's daily note, or ErrNotFound if it does not exist yet.
func (v *Vault) Today() (string, error) {
	return v.Page("daily/" + v.now().Format("2006-01-02"))
}

// Capture is one thing sent from the iPad.
type Capture struct {
	Kind string // "text", "link" or "photo"
	Text string // the note, the URL, or a caption
	// Photo, if set, is stored next to the note. Name is the client's file name (only its
	// extension is kept).
	Photo     io.Reader
	PhotoName string
	From      string // who sent it (the tailnet login), recorded in the note
}

// MaxPhoto bounds a photo upload.
const MaxPhoto = 25 << 20

// Save writes a capture into raw/inbox as a new markdown file (plus the photo, if any) and
// returns the note's path relative to the vault. /learn files it into the wiki later.
func (v *Vault) Save(c Capture) (string, error) {
	switch c.Kind {
	case "text", "link", "photo":
	default:
		return "", fmt.Errorf("brain: unknown capture kind %q", c.Kind)
	}
	if strings.TrimSpace(c.Text) == "" && c.Photo == nil {
		return "", errors.New("brain: empty capture")
	}
	now := v.now()
	inbox := filepath.Join(v.Root, "raw", "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		return "", err
	}
	base := fmt.Sprintf("%s_tether-%s-%s", now.Format("2006-01-02_1504"), c.Kind, slug(c.Text))
	var photoRef string
	if c.Photo != nil {
		ext := strings.ToLower(filepath.Ext(c.PhotoName))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".heic", ".webp":
		default:
			ext = ".bin"
		}
		name, f, err := createUnique(inbox, base, ext)
		if err != nil {
			return "", err
		}
		n, err := io.Copy(f, io.LimitReader(c.Photo, MaxPhoto+1))
		f.Close()
		if err == nil && n > MaxPhoto {
			err = fmt.Errorf("brain: photo over %d MB", MaxPhoto>>20)
		}
		if err != nil {
			os.Remove(filepath.Join(inbox, name))
			return "", err
		}
		photoRef = name
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\nsource: tether\nkind: %s\ncaptured: %s\n", c.Kind, now.Format(time.RFC3339))
	if c.From != "" {
		fmt.Fprintf(&b, "from: %q\n", c.From)
	}
	b.WriteString("---\n\n")
	switch {
	case c.Kind == "link":
		fmt.Fprintf(&b, "<%s>\n", strings.TrimSpace(c.Text))
	case strings.TrimSpace(c.Text) != "":
		b.WriteString(strings.TrimSpace(c.Text) + "\n")
	}
	if photoRef != "" {
		fmt.Fprintf(&b, "\n![photo](%s)\n", photoRef)
	}
	name, f, err := createUnique(inbox, base, ".md")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(b.String()); err != nil {
		return "", err
	}
	return filepath.Join("raw", "inbox", name), f.Sync()
}

// Search runs the vault's own search tool and returns its text output.
func (v *Vault) Search(ctx context.Context, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 200 {
		return "", errors.New("brain: query must be 1 to 200 characters")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	py := v.Python
	if py == "" {
		py = "python3"
	}
	// "--" ends option parsing, so a query starting with "-" is still a query.
	cmd := exec.CommandContext(ctx, py, filepath.Join(v.Root, "tools", "search.py"), "-n", "8", "--", query)
	cmd.Dir = v.Root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("brain: search: %w", err)
	}
	return string(out), nil
}

func (v *Vault) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slug makes a short, safe file-name fragment from the start of the text.
func slug(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	s = strings.Trim(nonSlug.ReplaceAllString(s, "-"), "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	if s == "" {
		return "note"
	}
	return s
}

// createUnique creates base+ext, or base-2+ext and so on, never overwriting an existing file.
func createUnique(dir, base, ext string) (string, *os.File, error) {
	for i := 1; i < 100; i++ {
		name := base + ext
		if i > 1 {
			name = fmt.Sprintf("%s-%d%s", base, i, ext)
		}
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return name, f, err
	}
	return "", nil, errors.New("brain: too many captures with the same name")
}
