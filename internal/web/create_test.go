package web_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"agent-manager/internal/apiclient"
	"agent-manager/internal/web/authoring"
	"agent-manager/internal/web/fakeapi"
)

func postDraft(t *testing.T, h http.Handler, target string, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for name, value := range fields {
		require.NoError(t, form.WriteField(name, value))
	}
	require.NoError(t, form.Close())

	req := httptest.NewRequest(http.MethodPost, target, &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "am_session", Value: "screen-test-session"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func draftJSON(t *testing.T, files ...authoring.File) string {
	t.Helper()
	raw, err := json.Marshal(files)
	require.NoError(t, err)
	return string(raw)
}

var validSkill = authoring.File{Path: "SKILL.md", Content: "---\nname: drafted\ndescription: Drafted in the browser.\n---\nDo it.\n"}

// archiveSent unzips the archive part of the one call the api received.
func archiveSent(t *testing.T, api *fakeapi.API, operation string) (form *multipart.Form, files map[string]string) {
	t.Helper()
	calls := api.Calls(operation)
	require.Len(t, calls, 1)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(calls[0].Body))
	req.Header = calls[0].Header
	require.NoError(t, req.ParseMultipartForm(1<<20))

	part := req.MultipartForm.File["archive"]
	require.Len(t, part, 1)
	require.Equal(t, "draft.zip", part[0].Filename, "the api picks the extractor from the extension")
	f, err := part[0].Open()
	require.NoError(t, err)
	raw, err := io.ReadAll(f)
	require.NoError(t, err)

	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	require.NoError(t, err)
	files = map[string]string{}
	for _, member := range reader.File {
		rc, err := member.Open()
		require.NoError(t, err)
		body, err := io.ReadAll(rc)
		require.NoError(t, err)
		files[member.Name] = string(body)
	}
	return req.MultipartForm, files
}

var seedPattern = regexp.MustCompile(`(?s)<script id="am-editor-seed" type="application/json">(.*?)</script>`)

func editorSeed(t *testing.T, body string) authoring.Seed {
	t.Helper()
	match := seedPattern.FindStringSubmatch(body)
	require.Len(t, match, 2, "the editor seed is missing")
	var seed authoring.Seed
	require.NoError(t, json.Unmarshal([]byte(match[1]), &seed))
	return seed
}

func paths(files []authoring.File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

func TestTheCatalogOffersCreateBesideUploadAndAddSource(t *testing.T) {
	_, h := newServer(t)
	body := get(t, h, "/catalog").Body.String()
	require.Contains(t, body, `<a class="am-btn" href="/catalog/create" id="catalog-create">Create</a>`)
}

func TestTheCreatePageOpensOnAStarter(t *testing.T) {
	_, h := newServer(t)

	t.Run("a standalone skill by default", func(t *testing.T) {
		rec := get(t, h, "/catalog/create")
		require.Equal(t, http.StatusOK, rec.Code)
		seed := editorSeed(t, rec.Body.String())
		require.Equal(t, "create", seed.Key)
		require.Equal(t, []string{"SKILL.md"}, paths(seed.Files))
		require.NotEmpty(t, seed.Examples)
		for _, e := range seed.Examples {
			require.NotEmptyf(t, seed.Bodies[e.ID], "example %s has no body", e.ID)
		}
	})

	t.Run("the starter the link names", func(t *testing.T) {
		seed := editorSeed(t, get(t, h, "/catalog/create?starter=plugin-mcp").Body.String())
		require.ElementsMatch(t, []string{"mcp.json", "plugin.json", "skills/example-skill/SKILL.md"}, paths(seed.Files))
	})

	t.Run("every starter is offered", func(t *testing.T) {
		body := get(t, h, "/catalog/create").Body.String()
		for _, s := range authoring.Starters {
			require.Contains(t, body, `href="/catalog/create?starter=`+s.ID+`"`)
		}
	})

	t.Run("signed out goes to sign-in", func(t *testing.T) {
		rec := getSignedOut(t, h, "/catalog/create")
		require.Equal(t, http.StatusFound, rec.Code)
	})
}

func TestValidatingADraftPreviewsItsArchiveWithoutRegistering(t *testing.T) {
	api, h := newServer(t)
	script := authoring.File{Path: "scripts/run.sh", Content: "#!/bin/sh\necho hi\n"}

	rec := postDraft(t, h, "/catalog/create/preview", map[string]string{"files": draftJSON(t, validSkill, script)})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "import-preview")

	_, files := archiveSent(t, api, "previewPackage")
	require.Equal(t, map[string]string{"SKILL.md": validSkill.Content, "scripts/run.sh": script.Content}, files)
	require.Empty(t, api.Calls("registerPackage"))
}

func TestPublishingADraftRegistersItAsAnUpload(t *testing.T) {
	api, h := registerServer(t)

	rec := postDraft(t, h, "/catalog/create", map[string]string{
		"files":      draftJSON(t, validSkill),
		"publisher":  "example/platform",
		"name":       "drafted",
		"version":    "1.0.0",
		"visibility": "organisation",
		"tags":       "one, two",
	})
	require.Equal(t, http.StatusOK, rec.Code)

	form, files := archiveSent(t, api, "registerPackage")
	require.Equal(t, map[string]string{"SKILL.md": validSkill.Content}, files)
	require.Equal(t, []string{"upload"}, form.Value["source"])
	require.Equal(t, []string{"drafted"}, form.Value["name"])
	require.Equal(t, []string{"1.0.0"}, form.Value["version"])
	require.Equal(t, []string{"one, two"}, form.Value["tags"])

	body := rec.Body.String()
	require.Contains(t, body, "amDraftClear", "a published draft must not be restored on the next visit")
	require.Contains(t, body, "/catalog?id=example%2Fthing%401.0.0&notice=registered")
}

func TestARefusedPublishKeepsTheDraftAndSaysWhy(t *testing.T) {
	api, h := newServer(t)
	api.Handle("registerPackage", func(w http.ResponseWriter, _ *http.Request) {
		fakeapi.Problem(w, http.StatusUnprocessableEntity, "version 1.0.0 already exists")
	})

	rec := postDraft(t, h, "/catalog/create", map[string]string{"files": draftJSON(t, validSkill), "version": "1.0.0"})
	body := rec.Body.String()
	require.Contains(t, body, "version 1.0.0 already exists")
	require.NotContains(t, body, "amDraftClear")
}

func TestABadDraftIsRefusedBeforeTheApiSeesIt(t *testing.T) {
	api, h := newServer(t)
	for name, files := range map[string]string{
		"no files":        "[]",
		"escaping path":   draftJSON(t, authoring.File{Path: "../SKILL.md", Content: "x"}),
		"absolute path":   draftJSON(t, authoring.File{Path: "/etc/passwd", Content: "x"}),
		"not json at all": "SKILL.md",
	} {
		t.Run(name, func(t *testing.T) {
			rec := postDraft(t, h, "/catalog/create", map[string]string{"files": files})
			require.Contains(t, rec.Body.String(), "import-result")
		})
	}
	require.Empty(t, api.Calls("registerPackage"))
	require.Empty(t, api.Calls("previewPackage"))
}

func setSource(api *fakeapi.API, id string, kind apiclient.PackageSourceKind) {
	detail := api.Data.Details[id]
	detail.Source = &apiclient.PackageSource{Kind: kind}
	if kind != apiclient.PackageSourceKindUpload {
		url := "https://github.com/example/platform-toolkit"
		detail.Source.Url = &url
	}
	api.Data.Details[id] = detail
}

func TestANewVersionOpensTheEditorOnTheLatestFiles(t *testing.T) {
	const id = "example/platform-toolkit"
	api, h := newServer(t)
	setSource(api, id, apiclient.PackageSourceKindUpload)
	list := api.Data.Files[id]
	list.Files = append(list.Files, apiclient.PackageFile{Path: "skills/adr-writer/assets/logo.png", Kind: apiclient.PackageFileKindBinary})
	api.Data.Files[id] = list

	t.Run("the package page links to it", func(t *testing.T) {
		require.Contains(t, get(t, h, "/packages/"+id).Body.String(), `href="/packages/`+id+`/new-version"`)
	})

	rec := get(t, h, "/packages/"+id+"/new-version")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	seed := editorSeed(t, body)
	require.ElementsMatch(t, []string{"plugin.json", "skills/adr-writer/SKILL.md"}, paths(seed.Files))
	require.Contains(t, seed.Key, id, "a version draft must not collide with a new-package draft")

	t.Run("the next patch is proposed and the identity is fixed", func(t *testing.T) {
		require.Equal(t, "1.3.0", api.Data.Details[id].Version)
		require.Regexp(t, `id="create-version"[^>]*value="1\.3\.1"`, body)
		require.Regexp(t, `id="create-publisher"[^>]*readonly`, body)
		require.Regexp(t, `id="create-name"[^>]*readonly`, body)
	})

	t.Run("a binary file is named, not silently dropped", func(t *testing.T) {
		require.Contains(t, body, `id="create-skipped"`)
		require.Contains(t, body, "skills/adr-writer/assets/logo.png")
	})
}

func TestAPackageFromASourceIsNotEditedInTheBrowser(t *testing.T) {
	const id = "example/platform-toolkit"
	api, h := newServer(t)
	setSource(api, id, apiclient.PackageSourceKindGit)

	require.NotContains(t, get(t, h, "/packages/"+id).Body.String(), "/new-version")

	rec := get(t, h, "/packages/"+id+"/new-version")
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), `id="create-refusal"`)
	require.NotContains(t, rec.Body.String(), "am-editor-seed")
}
