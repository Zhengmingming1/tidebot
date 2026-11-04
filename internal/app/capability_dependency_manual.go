package app

import (
	"net/url"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/capability"
)

type capabilityManualDependency struct {
	OS       string   `json:"os"`
	Docs     string   `json:"docs"`
	Commands []string `json:"commands,omitempty"`
	Note     string   `json:"note"`
}

// These commands are display-only trusted product copy, never parsed or executed
// by an installer. Recipe-supplied docs are links only, not executable commands.
func dependencyManualInstructions(req capability.ToolRequirement) []capabilityManualDependency {
	switch req.Name {
	case "dws":
		docs := "https://github.com/DingTalk-Real-AI/dingtalk-workspace-cli/releases"
		unix := "Prefer the matching official release archive: verify it, extract dws into your own bin directory, and configure its absolute path. If you instead choose the official shell installer, download and review it as install.sh first, then use the command below. DWS_NO_SKILLS=1 prevents installer skill setup. Do not run dws skill setup. tidebot never runs this command."
		return []capabilityManualDependency{
			{OS: "darwin", Docs: docs, Commands: []string{"DWS_NO_SKILLS=1 bash ./install.sh", "dws --version"}, Note: unix},
			{OS: "linux", Docs: docs, Commands: []string{"DWS_NO_SKILLS=1 bash ./install.sh", "dws --version"}, Note: unix},
			{OS: "windows", Docs: docs, Commands: []string{"$env:DWS_NO_SKILLS = '1'", ".\\dws.exe --version"}, Note: "Download and verify the Windows release ZIP, extract dws.exe into your own bin directory, and configure its absolute path. Use the extracted binary rather than a skill-setup installer. Do not run dws skill setup. These PowerShell commands only set the safety flag and check the extracted binary."},
		}
	case "gh":
		return []capabilityManualDependency{
			{OS: "darwin", Docs: "https://cli.github.com/manual/installation", Commands: []string{"brew install gh", "gh --version"}, Note: "Install with Homebrew if you already use it, or download the official release archive."},
			{OS: "linux", Docs: "https://github.com/cli/cli/blob/trunk/docs/install_linux.md", Commands: []string{"gh --version"}, Note: "Follow the official instructions for your distribution, or extract the release archive into your own bin directory. Then configure the executable path and recheck."},
			{OS: "windows", Docs: "https://cli.github.com/manual/installation", Commands: []string{"winget install --id GitHub.cli --exact", "gh --version"}, Note: "Use winget if available, or download the official release ZIP."},
		}
	case "node", "npm":
		return []capabilityManualDependency{
			{OS: "darwin", Docs: "https://nodejs.org/en/download", Commands: []string{"brew install node", "node --version", "npm --version"}, Note: "Choose a Node version compatible with the dependency. The official Node installer includes npm; Homebrew is an alternative if already available."},
			{OS: "linux", Docs: "https://nodejs.org/en/download", Commands: []string{"node --version", "npm --version"}, Note: "Download a compatible official Node archive, verify SHASUMS256.txt, extract into your own directory, and configure node/npm paths."},
			{OS: "windows", Docs: "https://nodejs.org/en/download", Commands: []string{"winget install --id OpenJS.NodeJS.LTS --exact", "node --version", "npm --version"}, Note: "Use the official Node installer or winget. Choose another maintained version if the capability's version constraint requires it."},
		}
	case "python", "python3":
		return []capabilityManualDependency{
			{OS: "darwin", Docs: "https://www.python.org/downloads/macos/", Commands: []string{"brew install python", "python3 --version"}, Note: "Use the official Python installer or existing Homebrew. Configure the interpreter path if it is not found."},
			{OS: "linux", Docs: "https://docs.python.org/3/using/unix.html", Commands: []string{"python3 --version"}, Note: "Install a compatible Python version through your distribution's package manager, then configure its absolute path."},
			{OS: "windows", Docs: "https://www.python.org/downloads/windows/", Commands: []string{"python --version"}, Note: "Use the official Python installer. Set python3's configured path to the installed python.exe if the python3 alias is unavailable."},
		}
	case "uv":
		return []capabilityManualDependency{
			{OS: "darwin", Docs: "https://docs.astral.sh/uv/getting-started/installation/", Commands: []string{"brew install uv", "uv --version"}, Note: "Use existing Homebrew, or download the verified official standalone archive."},
			{OS: "linux", Docs: "https://docs.astral.sh/uv/getting-started/installation/", Commands: []string{"uv --version"}, Note: "Download and verify the matching standalone release archive, extract uv into your own bin directory, and configure its absolute path. No curl-pipe installer is run by tidebot."},
			{OS: "windows", Docs: "https://docs.astral.sh/uv/getting-started/installation/", Commands: []string{"winget install --id astral-sh.uv --exact", "uv --version"}, Note: "Use winget if available, or extract the official standalone Windows ZIP."},
		}
	case "docker", "podman":
		docs := "https://docs.docker.com/engine/install/"
		if req.Name == "podman" {
			docs = "https://podman.io/docs/installation"
		}
		return manualAllPlatforms(docs, req.Name+" --version", "Install and start the container engine yourself using the official platform instructions. tidebot does not install a container engine or change Docker/WSL settings.")
	}
	for _, recipe := range req.Recipes {
		u, err := url.Parse(recipe.Docs)
		if err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && !strings.ContainsAny(recipe.Docs, "\r\n\x00") {
			return manualAllPlatforms(recipe.Docs, "", "Source-provided documentation link. Review the publisher and installation steps yourself, configure the executable path, and recheck. tidebot does not execute upstream commands.")
		}
	}
	return nil
}
func manualAllPlatforms(docs, command, note string) []capabilityManualDependency {
	out := make([]capabilityManualDependency, 0, 3)
	for _, os := range []string{"darwin", "linux", "windows"} {
		item := capabilityManualDependency{OS: os, Docs: docs, Note: note}
		if command != "" {
			item.Commands = []string{command}
		}
		out = append(out, item)
	}
	return out
}
