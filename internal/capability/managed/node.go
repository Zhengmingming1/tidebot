package managed

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// NodeLayout only supports official binary bundles; never source tarballs or
// installers, and never flattens npm's relative module layout.
func NodeLayout(version, goos, arch string) (asset, node, npm, format string, err error) {
	if !pinned(version) || strings.HasPrefix(version, "v") || strings.ContainsAny(version, "+-") {
		err = errors.New("Node requires an exact stable version")
		return
	}
	if goos != "linux" && goos != "darwin" && goos != "windows" || arch != "amd64" && arch != "arm64" {
		err = errors.New("unsupported Node platform")
		return
	}
	a := arch
	if a == "amd64" {
		a = "x64"
	}
	platform := goos
	if platform == "windows" {
		platform = "win"
	}
	root := "node-v" + version + "-" + platform + "-" + a
	format = "tar.gz"
	node = root + "/bin/node"
	npm = root + "/bin/npm"
	if goos == "windows" {
		format = "zip"
		node = root + "/node.exe"
		npm = root + "/npm.cmd"
	}
	asset = root + "." + format
	return
}
func (i *Installer) InstallNode(ctx context.Context, version string, asset Asset) (Result, error) {
	goos, arch := i.platform()
	name, node, npm, format, err := NodeLayout(version, goos, arch)
	if err != nil {
		return Result{}, err
	}
	if asset.URL != "https://nodejs.org/dist/v"+version+"/"+name || asset.Format != format {
		return Result{}, errors.New("Node archive must match official pinned release layout")
	}
	return i.InstallArchive(ctx, ArchiveRecipe{Name: "node", Version: version, Backend: "http-archive", Runtime: true, nodeBundle: true, Assets: map[string]Asset{goos + "-" + arch: asset}, Executables: map[string]string{"node": node, "npm": npm}})
}

// Ignore only documented launch links, then create our own npm/npx launchers.
// No archive-provided symlink is ever materialized or followed during extraction.
func knownNodeLink(name, target string) bool {
	parts := strings.Split(name, "/")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "node-v") || parts[1] != "bin" {
		return false
	}
	switch parts[2] {
	case "npm":
		return target == "../lib/node_modules/npm/bin/npm-cli.js"
	case "npx":
		return target == "../lib/node_modules/npm/bin/npx-cli.js"
	case "corepack":
		return target == "../lib/node_modules/corepack/dist/corepack.js"
	}
	return false
}
func writeNodeLaunchers(stage, version, goos, arch string) error {
	_, node, npm, _, err := NodeLayout(version, goos, arch)
	if err != nil {
		return err
	}
	root := path.Dir(path.Dir(node))
	moduleRoot := root + "/lib/node_modules/npm/bin"
	if goos == "windows" {
		moduleRoot = path.Dir(node) + "/node_modules/npm/bin"
	}
	for _, name := range []string{"npm-cli.js", "npx-cli.js"} {
		info, e := os.Lstat(filepath.Join(stage, filepath.FromSlash(moduleRoot+"/"+name)))
		if e != nil || !info.Mode().IsRegular() {
			return errors.New("Node bundle missing npm CLI modules")
		}
	}
	if goos == "windows" {
		return nil
	}
	for _, cmd := range []string{"npm", "npx"} {
		body := "#!/bin/sh\nbasedir=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd) || exit 1\nexec \"$basedir/node\" \"$basedir/../lib/node_modules/npm/bin/" + cmd + "-cli.js\" \"$@\"\n"
		f, e := os.OpenFile(filepath.Join(stage, filepath.FromSlash(path.Dir(npm)+"/"+cmd)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
		if e != nil {
			return e
		}
		_, e = f.WriteString(body)
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
	}
	return nil
}
