// Package buildinfo holds values stamped at link time (-ldflags "-X ...").
package buildinfo

import "strings"

// Version is the release version (e.g. "0.1.0"); "dev" for local builds.
var Version = "dev"

// Commit is the git commit the binary was built from.
var Commit = ""

// Official is "1" only in binaries built by the release workflow. The source is
// public, so anyone can build tidebot. A copy without this mark never replaces
// itself with downloaded binaries; it only announces new releases. Forks that run
// their own update channel set it together with their own updater.ReleasesRepo
// and updater.PublicKey (see docs/building.md).
var Official = ""

// SourceURL is where this build's source lives (AGPL-3.0 §13: the WebUI links it).
// Forks that ship a changed version must point this at their own source.
var SourceURL = "https://github.com/Zhengmingming1/tidebot"

// IsOfficial reports whether this binary came from the official release workflow.
func IsOfficial() bool { return Official == "1" || Official == "true" }

// SourceRef is the exact source of this build: the release tag, else the commit,
// else the repository itself.
func SourceRef() string {
	base := strings.TrimRight(SourceURL, "/")
	switch {
	case Version != "" && Version != "dev":
		return base + "/tree/v" + strings.TrimPrefix(Version, "v")
	case Commit != "":
		return base + "/tree/" + Commit
	}
	return base
}
