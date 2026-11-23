package web

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/gin-gonic/gin"
	"golang.org/x/mod/semver"

	"agent-manager/internal/web/view"
)

// cliPlatforms is every platform cli/.goreleaser.yaml publishes, in display order.
// It is also the allow-list for the download route, so a request can only ever
// name a file this scan produced.
var cliPlatforms = []view.CLIPlatform{
	{OS: "darwin", Arch: "arm64", Label: "macOS · Apple silicon"},
	{OS: "darwin", Arch: "amd64", Label: "macOS · Intel"},
	{OS: "linux", Arch: "amd64", Label: "Linux · x86-64"},
	{OS: "linux", Arch: "arm64", Label: "Linux · ARM64"},
}

var cliArchiveName = regexp.MustCompile(`^amctl_(.+)_(darwin|linux)_(amd64|arm64)\.tar\.gz$`)

type cliArchive struct {
	version string
	path    string
	name    string
}

// cliArchives maps "os/arch" to the newest archive in dir. It reads the directory
// on every call so archives replaced in place are picked up without a restart.
func cliArchives(dir string) (map[string]cliArchive, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	found := map[string]cliArchive{}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		m := cliArchiveName.FindStringSubmatch(entry.Name())
		if m == nil {
			continue
		}
		key := m[2] + "/" + m[3]
		candidate := cliArchive{version: m[1], path: filepath.Join(dir, entry.Name()), name: entry.Name()}
		if current, ok := found[key]; !ok || semver.Compare("v"+candidate.version, "v"+current.version) > 0 {
			found[key] = candidate
		}
	}
	return found, nil
}

// cliDownloads is what the install dialog lists, in cliPlatforms order.
func (s *Server) cliDownloads(c *gin.Context) []view.CLIDownload {
	archives, err := cliArchives(s.opts.CLIDistDir)
	if err != nil {
		logFrom(c).Error().Err(err).Str("dir", s.opts.CLIDistDir).Msg("read amctl archive directory")
		return nil
	}
	var downloads []view.CLIDownload
	for _, p := range cliPlatforms {
		archive, ok := archives[p.OS+"/"+p.Arch]
		if !ok {
			continue
		}
		downloads = append(downloads, view.CLIDownload{CLIPlatform: p, Version: archive.version})
	}
	return downloads
}

func (s *Server) downloadCLI(c *gin.Context) {
	goos, arch := c.Param("os"), c.Param("arch")
	known := false
	for _, p := range cliPlatforms {
		if p.OS == goos && p.Arch == arch {
			known = true
			break
		}
	}
	if !known {
		c.String(http.StatusNotFound, "amctl is not built for %s/%s\n", goos, arch)
		return
	}

	archives, err := cliArchives(s.opts.CLIDistDir)
	if err != nil {
		logFrom(c).Error().Err(err).Str("dir", s.opts.CLIDistDir).Msg("read amctl archive directory")
		c.String(http.StatusServiceUnavailable, "this hub cannot read its amctl archives\n")
		return
	}
	archive, ok := archives[goos+"/"+arch]
	if !ok {
		c.String(http.StatusNotFound, "this hub serves no amctl archive for %s/%s\n", goos, arch)
		return
	}
	c.FileAttachment(archive.path, archive.name)
}
