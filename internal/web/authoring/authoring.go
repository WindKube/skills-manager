// Package authoring backs the in-browser package editor: the starters it opens
// on, the example files it offers, and turning an edited draft into the archive
// the upload path already knows how to register.
package authoring

import (
	"archive/zip"
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"time"
)

//go:embed starters examples
var content embed.FS

const (
	MaxFiles = 200
	// MaxFileBytes and MaxDraftBytes keep a draft far below the api's 25 MB
	// archive cap: a draft is typed text, not a vendored tree.
	MaxFileBytes  = 512 << 10
	MaxDraftBytes = 4 << 20
	maxPathLength = 255
)

var ErrDraft = errors.New("draft")

var segmentPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

// File is one editor buffer.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Starter is a whole tree the editor can open on.
type Starter struct {
	ID          string
	Label       string
	Description string
}

// Example is one file the editor can add to whatever is already open.
type Example struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
	Path  string `json:"path"`

	source string
}

var Starters = []Starter{
	{ID: "skill", Label: "Skill", Description: "A standalone SKILL.md."},
	{ID: "skill-script", Label: "Skill with a script", Description: "A SKILL.md that runs a helper from scripts/."},
	{ID: "plugin", Label: "Plugin", Description: "A plugin.json bundling one skill."},
	{ID: "plugin-mcp", Label: "Plugin with an MCP server", Description: "A plugin with one skill and an mcp.json."},
}

const DefaultStarter = "skill"

var Examples = []Example{
	{ID: "script", Label: "Helper script", Kind: "skill", Path: "scripts/run.sh", source: "script.sh"},
	{ID: "reference", Label: "Reference document", Kind: "skill", Path: "references/REFERENCE.md", source: "reference.md"},
	{ID: "asset", Label: "Static asset", Kind: "skill", Path: "assets/template.txt", source: "asset.txt"},
	{ID: "skill", Label: "Another skill", Kind: "plugin", Path: "skills/new-skill/SKILL.md", source: "SKILL.md"},
	{ID: "mcp", Label: "MCP server config", Kind: "plugin", Path: "mcp.json", source: "mcp.json"},
}

// StarterFiles returns a starter's tree, falling back to the default for an
// unknown id so a hand-edited URL still opens something.
func StarterFiles(id string) (string, []File) {
	if !knownStarter(id) {
		id = DefaultStarter
	}
	root := "starters/" + id
	var files []File
	_ = fs.WalkDir(content, root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, readErr := content.ReadFile(name)
		if readErr != nil {
			return readErr
		}
		files = append(files, File{Path: strings.TrimPrefix(name, root+"/"), Content: string(body)})
		return nil
	})
	return id, files
}

func knownStarter(id string) bool {
	for _, s := range Starters {
		if s.ID == id {
			return true
		}
	}
	return false
}

// ExampleContent is an example's body, keyed by id for the editor's script.
func ExampleContent() map[string]string {
	out := make(map[string]string, len(Examples))
	for _, e := range Examples {
		body, err := content.ReadFile("examples/" + e.source)
		if err != nil {
			panic("authoring: embedded example is missing: " + e.source)
		}
		out[e.ID] = string(body)
	}
	return out
}

// Seed is what the editor script starts from, serialised into the page.
type Seed struct {
	Key      string            `json:"key"`
	Files    []File            `json:"files"`
	Examples []Example         `json:"examples"`
	Bodies   map[string]string `json:"bodies"`
}

func (s Seed) JSON() string {
	encoded, err := json.Marshal(s)
	if err != nil {
		panic("authoring: encode seed: " + err.Error())
	}
	return string(encoded)
}

// ParseFiles reads the editor's hidden field and checks every path before any
// archive is built from it.
func ParseFiles(raw string) ([]File, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("%w: the draft has no files", ErrDraft)
	}
	var files []File
	if err := json.Unmarshal([]byte(raw), &files); err != nil {
		return nil, fmt.Errorf("%w: the draft could not be read", ErrDraft)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: the draft has no files", ErrDraft)
	}
	if len(files) > MaxFiles {
		return nil, fmt.Errorf("%w: a draft holds at most %d files", ErrDraft, MaxFiles)
	}

	seen := make(map[string]struct{}, len(files))
	total := 0
	for i := range files {
		path, err := CleanPath(files[i].Path)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[strings.ToLower(path)]; dup {
			return nil, fmt.Errorf("%w: %s appears twice", ErrDraft, path)
		}
		seen[strings.ToLower(path)] = struct{}{}
		if len(files[i].Content) > MaxFileBytes {
			return nil, fmt.Errorf("%w: %s is over %d KB", ErrDraft, path, MaxFileBytes>>10)
		}
		total += len(files[i].Content)
		files[i].Path = path
	}
	if total > MaxDraftBytes {
		return nil, fmt.Errorf("%w: the draft is over %d MB", ErrDraft, MaxDraftBytes>>20)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// CleanPath refuses rather than rewrites: a path the person typed that we
// silently changed would publish somewhere they did not look.
func CleanPath(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", fmt.Errorf("%w: a file has no name", ErrDraft)
	}
	if len(path) > maxPathLength {
		return "", fmt.Errorf("%w: %.40s… is too long a path", ErrDraft, path)
	}
	for _, segment := range strings.Split(path, "/") {
		if !segmentPattern.MatchString(segment) || segment == ".." {
			return "", fmt.Errorf("%w: %q is not a usable path; use letters, digits, '.', '-' and '_' "+
				"between slashes", ErrDraft, path)
		}
	}
	return path, nil
}

// Zip builds the archive the upload path registers. Scripts are marked
// executable, since an agent runs them directly.
func Zip(files []File) ([]byte, error) {
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	modified := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, f := range files {
		header := &zip.FileHeader{Name: f.Path, Method: zip.Deflate, Modified: modified}
		mode := fs.FileMode(0o644)
		if executable(f) {
			mode = 0o755
		}
		header.SetMode(mode)
		w, err := archive.CreateHeader(header)
		if err != nil {
			return nil, fmt.Errorf("add %s to the archive: %w", f.Path, err)
		}
		if _, err := w.Write([]byte(f.Content)); err != nil {
			return nil, fmt.Errorf("write %s to the archive: %w", f.Path, err)
		}
	}
	if err := archive.Close(); err != nil {
		return nil, fmt.Errorf("close the archive: %w", err)
	}
	return buf.Bytes(), nil
}

func executable(f File) bool {
	return strings.HasPrefix(f.Content, "#!") ||
		strings.HasSuffix(f.Path, ".sh") ||
		strings.Contains("/"+f.Path, "/scripts/")
}
