// Package buildinfo holds values stamped at link time (-ldflags "-X ...").
package buildinfo

// Version is the release version (e.g. "0.1.0"); "dev" for local builds.
var Version = "dev"

// Commit is the git commit the binary was built from.
var Commit = ""
