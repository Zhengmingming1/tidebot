# Responsive workspace browser regression

Run from the repository root:

```sh
node internal/webui/testdata/responsive-workspaces.cjs
```

Requirements: Node.js, the `playwright` package, and Chromium. The default binary is
`/usr/bin/chromium`; set `CHROMIUM_PATH` to override it. No backend or external
service is used. The test loads the production CSS, renderers, and popup controller
with deterministic API fixtures and minimal application-shell helpers.

Coverage:

- English and Chinese at 390×844, 800×800, 1024×655, 1180×780, 1280×820, and 1440×900.
- Blank and active chat, 0/3/200 history entries, long titles, hover/focus, and local scrolling.
- Empty and populated workflow graphs, collapsed/expanded options, inspector placement,
  palette layout, local canvas scrolling, and keyboard zoom.
- An 80px provider trigger, `kilo-free`, very long option text, viewport bounds,
  keyboard selection, Escape, and Tab.

Screenshots and geometry/failure details go to `/tmp/tidebot-layout-qa`; set
`LAYOUT_SCREENSHOTS` to override this location. Screenshots are for local inspection
and are not checked in. The test is intentionally separate from `go test`, since
Chromium is not required for ordinary Go builds or unit tests.

If the environment forbids Chromium's local IPC socket, the browser can fail to
start with `process_singleton_posix.cc ... socket() failed: Operation not permitted`.
Report the browser stage as **blocked/not run**, not passed. Do not work around an
access restriction. The corresponding source/CSS checks remain available through:

```sh
go test ./internal/webui -run 'Test(WorkflowWorkspaceLayout|ChatHistoryOwnsItsScrollArea|SelectPopupFitsOptionText|SelectControlsClient|ResponsiveNavigationAndTables)' -count=1
```
