# Contributing

Thanks for looking! A few things keep reviews quick:

- Before a PR, run `go test ./...`, `go vet ./...`, `gofmt -l .` and `python3 scripts/check-i18n.py`.
  CI runs the same on Linux, macOS and Windows.
- Any UI text needs both `zh-CN.json` and `en.json` (the i18n check fails otherwise).
- In the WebUI, escape with `esc()`. No inline styles or scripts: the CSP blocks them. In Go, use
  parameterized SQL only.
- Never put real keys, tokens or webhook URLs in tests or fixtures. Use obvious fakes like
  `sk-test-…`. gitleaks runs on the history (`.gitleaksignore` lists the known fakes).
- Contributions are accepted under the project license, GNU AGPL-3.0-only.

Bigger changes: please open an issue first, so we can agree on the shape before you write it.

中文也可以，issue 和 PR 用哪种语言都行。
