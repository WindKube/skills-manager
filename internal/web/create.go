package web

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/starfederation/datastar-go/datastar"

	"agent-manager/internal/web/authoring"
	"agent-manager/internal/web/components"
	"agent-manager/internal/web/hub"
	"agent-manager/internal/web/view"
)

const draftFilename = "draft.zip"

func (s *Server) createPackage(c *gin.Context) {
	page, ok := s.load(c, view.CatalogQuery{})
	if !ok {
		return
	}
	if page.SignedOut {
		s.toSignIn(c)
		return
	}

	starter, files := authoring.StarterFiles(c.Query("starter"))
	s.render(c, http.StatusOK, "Create a package", "catalog", components.CreateScreen(components.Create{
		Starters:     authoring.Starters,
		Starter:      starter,
		Seed:         editorSeed("create", files),
		Version:      "1.0.0",
		Categories:   categoryNames(page),
		Visibilities: view.ImportVisibilityOptions(viewerGroups(c)),
	}))
}

// newVersion opens the editor on the latest version's files. Versions are
// immutable, so "editing" a package is publishing the next one.
func (s *Server) newVersion(c *gin.Context) {
	namespace, name := c.Param("namespace"), c.Param("name")
	detail, err := s.deps.Hub.Package(session(c), namespace, name)
	switch {
	case errors.Is(err, view.ErrSignedOut):
		s.toSignIn(c)
		return
	case errors.Is(err, view.ErrNotFound):
		s.render(c, http.StatusNotFound, "Package", "catalog", components.PackageScreen(view.Package{Missing: true}))
		return
	case err != nil:
		logFrom(c).Error().Err(err).Str("package", namespace+"/"+name).Msg("load package for a new version")
		c.Status(http.StatusBadGateway)
		return
	}

	screen := components.Create{
		PackageID:    detail.ID,
		Publisher:    detail.Publisher,
		Name:         detail.Name,
		Version:      nextPatch(detail.Version),
		From:         detail.Version,
		Categories:   []string{detail.Category},
		Category:     detail.Category,
		Visibility:   detail.Visibility,
		Visibilities: view.ImportVisibilityOptions(viewerGroups(c)),
	}
	if !view.EditableInBrowser(detail.Source) {
		screen.Refusal = "This package is fetched from " + detail.Source.URL + ". Its next version comes " +
			"from that source, not from an edit here."
		s.render(c, http.StatusConflict, "New version", "catalog", components.CreateScreen(screen))
		return
	}

	files, skipped, err := s.versionFiles(c, namespace, name)
	if err != nil {
		screen.Refusal = err.Error()
		s.render(c, http.StatusOK, "New version", "catalog", components.CreateScreen(screen))
		return
	}
	screen.Skipped = skipped
	screen.Seed = editorSeed("version:"+detail.ID+"@"+detail.Version, files)
	s.render(c, http.StatusOK, "New version of "+detail.Name, "catalog", components.CreateScreen(screen))
}

var errVersionUnreadable = errors.New("the latest version's files cannot be read yet; " +
	"a version is readable once it has been scanned and not rejected")

// versionFiles reads every text file of the latest version. Binary and
// over-limit files cannot be carried through a text editor, so they are named
// rather than silently dropped.
func (s *Server) versionFiles(c *gin.Context, namespace, name string) ([]authoring.File, []string, error) {
	list, err := s.deps.Hub.PackageFiles(session(c), namespace, name)
	switch {
	case errors.Is(err, hub.ErrForbidden):
		return nil, nil, errVersionUnreadable
	case err != nil:
		logFrom(c).Error().Err(err).Msg("list files for a new version")
		return nil, nil, errors.New("the hub could not be reached; try again")
	}

	var files []authoring.File
	var skipped []string
	for _, row := range list.Files {
		if row.OverLimit || row.Kind == "binary" {
			skipped = append(skipped, row.Path)
			continue
		}
		detail, err := s.deps.Hub.PackageFile(session(c), namespace, name, row.Path)
		switch {
		case errors.Is(err, hub.ErrFileTooLarge), errors.Is(err, hub.ErrFileNotRenderable):
			skipped = append(skipped, row.Path)
			continue
		case errors.Is(err, hub.ErrForbidden):
			return nil, nil, errVersionUnreadable
		case err != nil:
			logFrom(c).Error().Err(err).Str("path", row.Path).Msg("read a file for a new version")
			return nil, nil, errors.New("the hub could not be reached; try again")
		}
		files = append(files, authoring.File{Path: row.Path, Content: detail.Text})
	}
	return files, skipped, nil
}

func editorSeed(key string, files []authoring.File) authoring.Seed {
	return authoring.Seed{
		Key:      key,
		Files:    files,
		Examples: authoring.Examples,
		Bodies:   authoring.ExampleContent(),
	}
}

// nextPatch proposes x.y.z+1, and nothing for a version it cannot read, so the
// person types one rather than publishing a guess.
func nextPatch(version string) string {
	core, _, _ := strings.Cut(version, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return ""
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return ""
	}
	return parts[0] + "." + parts[1] + "." + strconv.Itoa(patch+1)
}

func (s *Server) createPreview(c *gin.Context) {
	if !s.parseImportForm(c) {
		return
	}
	archive, ok := s.draftArchive(c)
	if !ok {
		return
	}

	preview, err := s.deps.Hub.Preview(session(c), archive)
	if err != nil {
		logFrom(c).Error().Err(err).Msg("preview draft")
		s.patchImportProblem(c, "the hub could not be reached")
		return
	}
	sse := datastar.NewSSE(c.Writer, c.Request)
	if err := sse.PatchElementTempl(components.ImportResultBanner(nil)); err != nil {
		logFrom(c).Error().Err(err).Msg("clear the draft banner")
		return
	}
	if err := sse.PatchElementTempl(components.ImportPreviewPanel(&preview)); err != nil {
		logFrom(c).Error().Err(err).Msg("patch draft preview")
	}
}

func (s *Server) createPublish(c *gin.Context) {
	if !s.parseImportForm(c) {
		return
	}
	archive, ok := s.draftArchive(c)
	if !ok {
		return
	}

	result, err := s.deps.Hub.Register(session(c), view.Registration{
		Tab:        view.ImportUpload,
		Publisher:  c.PostForm("publisher"),
		Name:       c.PostForm("name"),
		Version:    c.PostForm("version"),
		Category:   c.PostForm("category"),
		Visibility: c.PostForm("visibility"),
		Tags:       c.PostForm("tags"),
		Archive:    &archive,
	})
	if err != nil {
		logFrom(c).Error().Err(err).Msg("publish draft")
		s.patchImportProblem(c, "the hub could not be reached")
		return
	}

	sse := datastar.NewSSE(c.Writer, c.Request)
	if !result.Registered {
		if err := sse.PatchElementTempl(components.ImportResultBanner(&result)); err != nil {
			logFrom(c).Error().Err(err).Msg("patch draft refusal")
			return
		}
		if result.Preview != nil {
			if err := sse.PatchElementTempl(components.ImportPreviewPanel(result.Preview)); err != nil {
				logFrom(c).Error().Err(err).Msg("patch draft preview")
			}
		}
		return
	}

	// The draft is only cleared once the hub has accepted it.
	if err := sse.ExecuteScript("window.amDraftClear && window.amDraftClear()"); err != nil {
		logFrom(c).Error().Err(err).Msg("clear the published draft")
		return
	}
	if err := sse.Redirect(publishedHref(result)); err != nil {
		logFrom(c).Error().Err(err).Msg("redirect to the published package")
	}
}

// publishedHref goes to the catalog with the queued notice: the package page
// itself may 404 until the fetcher has stored the first version.
func publishedHref(result view.ImportResult) string {
	return "/catalog?" + url.Values{"notice": {view.CatalogNoticeRegistered}, "id": {registeredID(result)}}.Encode()
}

func (s *Server) draftArchive(c *gin.Context) (view.Archive, bool) {
	files, err := authoring.ParseFiles(c.PostForm("files"))
	if err != nil {
		s.patchImportProblem(c, strings.TrimPrefix(err.Error(), authoring.ErrDraft.Error()+": "))
		return view.Archive{}, false
	}
	archive, err := authoring.Zip(files)
	if err != nil {
		logFrom(c).Error().Err(err).Msg("zip the draft")
		s.patchImportProblem(c, "the draft could not be packed")
		return view.Archive{}, false
	}
	return view.Archive{Filename: draftFilename, Size: int64(len(archive)), Content: bytes.NewReader(archive)}, true
}
