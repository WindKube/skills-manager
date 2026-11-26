package authoring_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"agent-manager/internal/bundle"
	"agent-manager/internal/domain/pkgspec"
	"agent-manager/internal/web/authoring"
)

// inspect runs a draft through the same extractor and spec checks the fetcher
// uses, so a starter that would be refused on publish fails here instead.
func inspect(t *testing.T, files []authoring.File) *pkgspec.Package {
	t.Helper()
	archive, err := authoring.Zip(files)
	require.NoError(t, err)
	tree, err := bundle.ExtractZip(context.Background(), bytes.NewReader(archive), int64(len(archive)), bundle.DefaultLimits())
	require.NoError(t, err)
	pkg, err := pkgspec.Inspect(tree, "")
	require.NoError(t, err)
	require.Empty(t, pkg.Layout.Dropped, "the layout filter would drop part of this draft")
	return pkg
}

func TestEveryStarterPublishesCleanlyWithEveryExampleOfItsKind(t *testing.T) {
	bodies := authoring.ExampleContent()
	for _, starter := range authoring.Starters {
		t.Run(starter.ID, func(t *testing.T) {
			id, files := authoring.StarterFiles(starter.ID)
			require.Equal(t, starter.ID, id)
			require.NotEmpty(t, files)

			kind := string(inspect(t, files).Kind)
			require.Equal(t, kind, strings.SplitN(starter.ID, "-", 2)[0])

			taken := map[string]bool{}
			for _, f := range files {
				taken[f.Path] = true
			}
			for _, example := range authoring.Examples {
				if example.Kind == kind && !taken[example.Path] {
					files = append(files, authoring.File{Path: example.Path, Content: bodies[example.ID]})
				}
			}
			inspect(t, files)
		})
	}
}

func TestAnUnknownStarterOpensTheDefault(t *testing.T) {
	id, files := authoring.StarterFiles("../../etc")
	require.Equal(t, authoring.DefaultStarter, id)
	require.NotEmpty(t, files)
}

func TestScriptsAreArchivedExecutable(t *testing.T) {
	archive, err := authoring.Zip([]authoring.File{
		{Path: "SKILL.md", Content: "---\nname: a\ndescription: b\n---\n"},
		{Path: "scripts/run.py", Content: "print(1)"},
	})
	require.NoError(t, err)
	tree, err := bundle.ExtractZip(context.Background(), bytes.NewReader(archive), int64(len(archive)), bundle.DefaultLimits())
	require.NoError(t, err)

	script, ok := tree.Lookup("scripts/run.py")
	require.True(t, ok)
	require.NotZero(t, script.Mode&0o100, "a script under scripts/ must be executable")
	manifest, ok := tree.Lookup("SKILL.md")
	require.True(t, ok)
	require.Zero(t, manifest.Mode&0o100)
}

func encode(t *testing.T, files []authoring.File) string {
	t.Helper()
	raw, err := json.Marshal(files)
	require.NoError(t, err)
	return string(raw)
}

func TestParseFilesRefusesPathsThatCouldEscapeOrSurprise(t *testing.T) {
	for _, path := range []string{
		"", "/SKILL.md", "../SKILL.md", "scripts/../../x", "a//b", "scripts\\run.sh",
		"scripts/", ".hidden", "scripts/ run.sh", strings.Repeat("a", 256),
	} {
		t.Run(path, func(t *testing.T) {
			_, err := authoring.ParseFiles(encode(t, []authoring.File{{Path: path, Content: "x"}}))
			require.ErrorIs(t, err, authoring.ErrDraft)
		})
	}
}

func TestParseFilesRefusesDuplicatesAndOversizedDrafts(t *testing.T) {
	_, err := authoring.ParseFiles(encode(t, []authoring.File{{Path: "SKILL.md"}, {Path: "skill.md"}}))
	require.ErrorIs(t, err, authoring.ErrDraft, "paths differing only by case collide on some filesystems")

	_, err = authoring.ParseFiles(encode(t, []authoring.File{
		{Path: "SKILL.md", Content: strings.Repeat("x", authoring.MaxFileBytes+1)},
	}))
	require.ErrorIs(t, err, authoring.ErrDraft)

	_, err = authoring.ParseFiles("[]")
	require.ErrorIs(t, err, authoring.ErrDraft)
	_, err = authoring.ParseFiles("not json")
	require.ErrorIs(t, err, authoring.ErrDraft)
}

func TestParseFilesSortsAndTrims(t *testing.T) {
	files, err := authoring.ParseFiles(encode(t, []authoring.File{
		{Path: " scripts/run.sh ", Content: "b"}, {Path: "SKILL.md", Content: "a"},
	}))
	require.NoError(t, err)
	require.Equal(t, []authoring.File{{Path: "SKILL.md", Content: "a"}, {Path: "scripts/run.sh", Content: "b"}}, files)
}
