# Managed dependency installation

This package executes **reviewed recipes only**. It does not discover versions,
approve installation, register capabilities, mutate user/system tools, or bind a
capability. The application owns those steps and must display the exact pinned
recipe and verification method before calling an installer.

- `ResolveSystemFirst` reuses a compatible system dependency unless explicitly
  pinned to managed. A resolver supplies detection/version policy.
- `InstallArchive` selects an explicit `os-arch` asset, requires SHA256, downloads
  with a timeout and 256 MiB limit, and extracts at most 20,000 entries / 500 MiB.
  ZIP, tar.gz and a single raw binary are supported. Unsafe paths, links, device
  entries and duplicate files fail closed. The caller declares executable paths.
- `InstallNPM` requires the complete reviewed npm lockfile v2/v3 and exact direct
  version. Every transitive artifact needs HTTPS and SHA256/SHA512 SRI. `npm ci`
  runs with scripts disabled and private config/cache/prefix. Package aliases are
  scanned using their actual locked name. Bin links cannot escape the prefix.
- `InstallUV` requires absolute resolved uv/Python paths and a complete pinned,
  hash-locked dependency closure. It builds a relocatable venv (requires uv with
  `--relocatable` support), installs wheels only without dependency resolution,
  checks the resulting dependency closure, and disables automatic Python
  downloads. `UV_CACHE_DIR` and `UV_PYTHON_INSTALL_DIR` stay under the data root.
- Npm/PyPI packages are checked for OSV `MAL-*` IDs/aliases. The default check
  fails closed on network/parse errors. Ordinary CVEs are not malware verdicts.

All installs stage within the destination filesystem, rename on success, and
remove their stage on failure. Existing versions are never overwritten. If a
later capability transaction fails, the caller removes only newly installed,
unreferenced directories. A result does not globally modify PATH. Package-manager
environment sizes are checked after execution; OS-level disk quotas are not
provided here. Shared tool/cache quotas and reference-aware GC are supplied by storage.go;
the application must pass all current and rollback references before cleanup.

`HTTPDoer`, `Executor`, `MalwareChecker`, and `SignatureVerifier` are injectable.
Use the application's proxy-aware HTTP client. A signature declaration without
an available verifier is an error. Minisign verification is supplied, but needs independently reviewed publisher
keys. No built-in cosign or GitHub attestation implementation is supplied: **never label checksum-only installs as
publisher authenticated**. Unsigned results explicitly say
`sha256-integrity-only`. Go-created downloads do not inherit browser quarantine;
this implementation does not change macOS/system quarantine settings.

The data directory must be private to tidebot and protected against concurrent
untrusted local writes. Ancestor symlinks are rejected. Windows npm command shims
use the system command processor with expansion/metacharacter rejection. Unit
fixtures use HTTP loopback; production artifact URLs require HTTPS.

Tests use local HTTP fixtures and fake executors. They do not install third-party
packages, acquire credentials, change system settings or exercise live services.
