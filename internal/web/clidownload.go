package web

import (
	"bufio"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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

var (
	cliArchiveName = regexp.MustCompile(`^amctl_(.+)_(darwin|linux)_(amd64|arm64)\.tar\.gz$`)
	sha256Hex      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type cliArchive struct {
	version string
	path    string
	name    string
	sha256  string
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

	sums, err := cliChecksums(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return nil, err
	}
	for key, archive := range found {
		archive.sha256 = sums[archive.name]
		found[key] = archive
	}
	return found, nil
}

// cliChecksums reads goreleaser's "<sha256>  <file>" lines. A missing file is no
// checksums, not an error; a malformed line is skipped rather than shown.
func cliChecksums(path string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec // a fixed name inside the operator-configured directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	sums := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || !sha256Hex.MatchString(fields[0]) {
			continue
		}
		sums[fields[1]] = fields[0]
	}
	return sums, scanner.Err()
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
		downloads = append(downloads, view.CLIDownload{CLIPlatform: p, Version: archive.version, SHA256: archive.sha256})
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
