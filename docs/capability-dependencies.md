# Dependency paths and manual setup

Extensions → Tools saves owner-only, per-command preferences. An absolute executable
path overrides normal command discovery. **Prefer managed** instead selects only a
compatible installation already owned and verified by tidebot, or offers a reviewed
managed download if a pinned recipe is available. It never authorizes downloading.
The managed setting takes precedence over a saved executable path.

A new proposal records these choices and the exact executable paths it found. Changing
settings does not change pending proposals, installed locks, or running processes.
For an installed capability, use **Rescan**, review the dependency plan, and confirm
its new version to adopt changed settings. Ready paths are rechecked at confirmation
and runtime; an incompatible or missing reviewed binary fails closed rather than
silently taking another command from PATH. Managed content is checked against its
stored executable and directory hashes. No preference is exported with a portable
capability bundle.

## Discovery and safety

Discovery checks the configured path, process PATH, safe static login-profile PATH
hints, and known platform directories. It caches non-runtime detection briefly;
**Check again**, a new proposal, and saving settings invalidate the cache. Runtime
checks never authorize execution from cached detection alone.

The static profile reader supports only files consisting of comments, blank lines,
and standalone literal PATH assignments, optionally using `$HOME`, `${HOME}`,
`$PATH`, or `${PATH}`. It reads bash's first available `.bash_profile`, `.bash_login`,
or `.profile`; zsh's `.zprofile`; or `.profile` for sh/dash. It never starts a login
shell, sources a file, executes substitutions, or reads interactive rc hooks. A
profile containing any other syntax is skipped entirely. This is deliberately not
full login-shell PATH evaluation. Configure the absolute tool path when a version
manager, conditional, startup hook, custom shell, or macOS `path_helper` supplies it.

Known directories include Homebrew and `~/.local/bin` on Unix, and Scoop shims,
WinGet links, npm, Node, and GitHub CLI directories on Windows. Version checks are
bounded, noninteractive, fixed flags; model tools cannot edit preferences.

## Manual fallback

Each dependency plan can contain `manual` entries with `os`, `docs`, `commands`, and
`note`. Official instructions are included for dws, gh, node/npm, Python, uv, and
container engines. Unknown tools expose a source-provided HTTPS documentation link
when available, with no source-supplied commands. Commands are display/copy only:
tidebot never runs them. Some platform entries give extraction instructions and
verification commands rather than a package-manager command that would assume a
particular distribution or installer.

For DWS, prefer the matching verified release archive and configure the extracted
binary path. If manually using its official shell installer, first download and
review it, then run `DWS_NO_SKILLS=1 bash ./install.sh`. On Windows, extract the
release ZIP and set `$env:DWS_NO_SKILLS = '1'` before checking the extracted binary.
Never use `dws skill setup` or a skills installer that writes agent directories.

## Owner API

- `GET /api/capabilities/dependency-preferences`: returns `preferences` and a scope note.
- `PUT` at the same URL: `{ "name": "dws", "path": "/absolute/path/dws", "prefer_managed": false }`.
- `DELETE` at the same URL: `{ "name": "dws" }`.

Writes require the owner's authenticated browser session and CSRF checks. These
operations are absent from the session-model IPC allowlist. Maximum 128 entries;
paths must be absolute and cannot contain control-line characters. Saving a path
performs no version check or other execution until the owner checks/proposes a
capability using it.
