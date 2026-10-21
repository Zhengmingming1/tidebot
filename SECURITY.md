# Security

Please don't open a public issue for a vulnerability. Report it privately instead:
**Security → Report a vulnerability** on
[github.com/Zhengmingming1/tidebot](https://github.com/Zhengmingming1/tidebot/security/advisories/new).
I'll answer within a few days. Once there's a fix release, I'll credit you if you want.

Especially interesting:

- the self-update path: `latest.json` signature, file checksums, replace and rollback;
- the WebUI's auth, CSRF and session handling, and the loopback/desktop bridges;
- the plugin sandbox and permission prompts;
- the SSRF guard used by plugin prefill and feed discovery (`internal/prefill`);
- how provider keys are stored and passed to the `claude` process.

Supported versions: the latest release. Updates are signed with ed25519. The public key is in
`internal/updater/manifest.go`.

中文报告也可以。
