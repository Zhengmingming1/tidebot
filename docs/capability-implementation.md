# Capability packages

This implementation keeps capability installation inside tidebot's data directory.
It does not invoke upstream install scripts or modify Claude/Codex/Cursor settings.
The optional P4 OCI execution and OS-level network sandbox are not enabled.

## Owner workflow

1. Use **Extensions → Discover** or the session's capability search tools.
2. Inspect the server-produced proposal, fixed source/version, dependency plan,
   scan findings and permissions. Confirm installation separately from dependency
   downloads. Unknown/high-risk sources remain subject to the trust matrix.
3. Bind the installed package to a session, task, workflow node or button.
4. Add credentials through the write-only credential form. For remote MCP,
   **Connect** discovers OAuth metadata, shows scopes and registration details,
   then requires an explicit authorization action.
5. After UI installation, a free dependency/MCP discovery check runs automatically.
   Use **Test** to repeat MCP initialize/tools-list, without invoking tools.
   Session MCP tools remain unavailable until their exact discovered names are
   approved in **External tools**. Metadata changes invalidate saved grants.
6. Browse a Claude marketplace by URL to select a locally commit-pinned entry or
   explicitly review a remote entry. Marketplace membership does not confer trust. Save marketplaces/official registry
   in Sources and explicitly refresh entries. Public skills.sh/GitHub search is
   opt-in, visibly sends the query, and never makes community results trusted.
   Rediscovering an installed marketplace entry creates a reviewed update card.
7. Use **Manage** for reviewed updates, rollback, export, old snapshot cleanup,
   and uninstall. Model-requested updates/uninstalls only create owner review cards.
   Uninstall pins the exact installed version and deletion summary, removes only
   exclusive unreferenced skill registrations, and preserves history and secrets.
   Bindings and active/historical references constrain cleanup.
8. Manage defaults for new sessions, deactivate bindings explicitly, and inspect
   exact versions in run details. A local rescan creates a fresh review rather than
   changing immutable versions. Paid smoke testing opens a bound session draft;
   only the owner sending that draft starts the paid turn.

## Credentials and remote authorization

Stdio credentials are scoped to their individual MCP server's environment, not
injected into the parent Claude process. Its run-local MCP config is created with
0600 permissions and deleted on cleanup. Remote bearer tokens stay in the secret
store and are obtained through the run-scoped headers helper, not written into
MCP config or CLI arguments. OAuth uses PKCE, one-time state, exact resource/
issuer/client binding and an authenticated owner callback. A stable callback URL
allows an already registered client to be reused.

**Disconnect and clear credentials** is an explicit separate operation before
uninstall. Remote OAuth is revoked before its local token is deleted; a failed
revocation retains that token for recovery. Shared capabilities are not affected.
Uninstall by itself retains credentials and snapshots, as disclosed in its dialog.
A persistent names-only credential index survives snapshot GC, so clearing also
finds OAuth grants belonging to removed historical components. Secret-store
failures are distinguished from absent values and never reported as success.

## Dependencies and storage

Compatible system executables are preferred and are never updated or removed.
Executable-path/managed preferences apply only to fresh reviewed locks, never
authorize downloads, and do not silently rewrite installed resolutions.
Managed archives require pinned hashes. npm uses a complete integrity-locked
closure and disabled lifecycle scripts; Python uses pinned, hash-verified wheels
without automatic dependency resolution at install time. Unsupported or ambiguous
recipes are shown as manual rather than silently resolved or executed.

Registry metadata resolution supports npm ranges, nested conflicts and validated
peer dependencies, and Python ranges, extras and target-specific environment
markers. Python wheels are selected against an isolated interpreter probe;
uncertain platform tags remain manual. Managed Node/npm comes from a pinned
official release bundle. A compatible system Node is reused with separate npm
launchers; otherwise the reviewed bundle supplies the runtime. Installs use
engine-strict checks and verify reusable managed trees before trusting them.

Tools and runtimes shows disk accounting and quotas. Cleanup is explicit and
only touches ownership-marked managed directories and old owned cache files.
Every persisted capability version (including rollback/history) retains its
managed references. Snapshot cleanup preserves current, one rollback, explicit
pins and historical run references; managed cleanup can then reclaim genuinely
unreferenced versions while keeping one spare version. System tools and unknown
unowned directories are preserved.

## Legacy extensions and local edits

Native `plugin.yaml` manifests are recognized and scanned. Their runtime,
connectors, triggers, notifiers and workflow packs remain disabled in capability
imports; existing native plugins remain visible under Installed and retain the
plugin manager’s individual approval workflow. Importing a capability is not
native-plugin execution approval.

Locally edited registered skills are marked. Update review requires explicit
keep-local, separate-copy or adopt-upstream choices, pins the observed local
version, and rejects stale choices. Immutable history remains available.

## Verification boundaries

Unit/integration fixtures cover source pinning, trust spoofing, install rollback,
credential isolation, OAuth replay/audience checks, exact tool grants, registry
updates and reference-safe GC. Curated Notion and Linear tests mock the complete
install → OAuth → discovery → grant path; they do not authorize a real account.
The hosted service implementation itself cannot be content-pinned, so the UI
explicitly distinguishes a pinned client manifest from mutable remote tools.

Live provider OAuth, DingTalk organization approval, real release downloads and
Claude execution require a suitably configured test account/host. Socket-based
daemon and rendered browser acceptance must run in an environment that permits
Unix sockets. Do not treat source/Node tests as screenshot acceptance.

Useful checks (source the workspace's toolchain setup where needed):

```sh
go test -count=1 ./...
go vet ./...
go test -race ./internal/capability/... ./internal/app -run 'TestCapability|TestCurated'
python3 scripts/build-webui.py --check
node --check internal/webui/static/app.js
python3 scripts/check-i18n.py
gofmt -l .
```

Official source references used for curated endpoints/asset layouts:
- Notion: https://developers.notion.com/guides/mcp/get-started-with-mcp
- Linear: https://linear.app/docs/mcp
- GitHub MCP release layout: https://github.com/github/github-mcp-server/blob/main/.goreleaser.yaml
- DWS release layout: https://github.com/DingTalk-Real-AI/dingtalk-workspace-cli/blob/main/.goreleaser.yaml
