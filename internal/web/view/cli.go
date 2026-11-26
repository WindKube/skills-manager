package view

import "time"

// PendingDeviceAuthorization is what the Connect-the-CLI screen shows before the viewer confirms.
type PendingDeviceAuthorization struct {
	RequestingHost string
	ExpiresAt      time.Time
}

type CLI struct {
	UserCode  string
	Pending   *PendingDeviceAuthorization
	Countdown string

	Unknown bool
	Expired bool
	Decided bool

	SignedOut   bool
	Unavailable bool

	Notice *Notice

	// Command is empty when HubURL is not configured.
	Command string
	HubURL  string

	// Downloads lists the platforms this hub holds an amctl archive for.
	Downloads []CLIDownload
	// DownloadBase is the browser-facing origin the install commands name; empty
	// shows download links only.
	DownloadBase string
}

type CLIPlatform struct {
	OS    string
	Arch  string
	Label string
}

type CLIDownload struct {
	CLIPlatform
	Version string
	// SHA256 is the archive's digest from goreleaser's checksums.txt; empty when it lists none.
	SHA256 string
}

func (d CLIDownload) Path() string { return "/cli/download/" + d.OS + "/" + d.Arch }

// InstallCommand extracts only the binary into the working directory.
func (d CLIDownload) InstallCommand(base string) string {
	return "curl -fsSL " + base + d.Path() + " | tar -xz amctl"
}

func (s CLI) Refused() bool { return s.Unknown || s.Expired || s.Decided }
