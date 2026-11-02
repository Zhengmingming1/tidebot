# WebUI feature sources

Edit the feature files in this directory, then run:

```sh
python3 scripts/build-webui.py
node --check internal/webui/static/app.js
python3 scripts/check-i18n.py
```

`bundle.json` defines evaluation order. The assembler preserves the existing private
closure, function bindings, event registration order, CSP and `/static/app.js` URL.
The checked-in bundle supports Go builds without a JavaScript toolchain. CI rejects
missing/duplicate source entries or a stale bundle. Do not edit `static/app.js` directly.

Feature boundaries cover the application shell, navigation, task and skill editors,
workflow canvas, providers, plugins, run logs, spend, settings and routing. Functions
currently share the original closure to keep this extraction compatible; this is a
source split, not a conversion to independently loaded ES modules.

Go API registration lives in `api.go`; DTOs and handlers are separated into
`api_dto.go`, `api_tasks.go`, `api_skills.go`, `api_runs.go`, `api_run_logs.go`,
`api_buttons.go`, `api_schedule.go`, `api_settings.go` and `api_errors.go`.
