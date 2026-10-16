# Model providers for Claude Code (tidebot)

Claude Code talks the Anthropic Messages API. Third-party backends work by pointing
Claude Code at an Anthropic-compatible base URL via environment variables, then
injecting those variables into the `claude` child process only (never into SQLite
plaintext or logs).

## How Claude Code selects the endpoint

| Variable | Role |
|---|---|
| `ANTHROPIC_BASE_URL` | Anthropic-compatible API root (no trailing slash). Empty = official Anthropic. |
| `ANTHROPIC_AUTH_TOKEN` | Bearer / auth token preferred by most third parties. |
| `ANTHROPIC_API_KEY` | Official Anthropic API key (x-api-key). Avoid setting both with third parties. |
| `ANTHROPIC_MODEL` | Primary model id |
| `ANTHROPIC_DEFAULT_OPUS_MODEL` | Opus-tier mapping |
| `ANTHROPIC_DEFAULT_SONNET_MODEL` | Sonnet-tier mapping |
| `ANTHROPIC_DEFAULT_HAIKU_MODEL` | Haiku-tier mapping |
| `ANTHROPIC_DEFAULT_FABLE_MODEL` | Fable-tier mapping (newer Claude Code) |
| `ANTHROPIC_SMALL_FAST_MODEL` | Small/fast helper model |
| `CLAUDE_CODE_SUBAGENT_MODEL` | Sub-agent model |

**magpie** (local gateway) writes the same variables into `~/.claude/settings.json`’s `env` block.
tidebot instead injects them only into the subprocess environment so the user’s global Claude Code
config is left alone. Sources: [magpie integrate docs](https://usemagpie.ai/docs/integrate),
[magpie Claude adapter source](https://github.com/yetone/magpie) (`internal/agent/claude.go`).

## Verified presets (official docs)

| Preset id | Anthropic-compatible base URL | Auth env | Source |
|---|---|---|---|
| `anthropic` | *(default — do not set `ANTHROPIC_BASE_URL`)* | `ANTHROPIC_API_KEY` | Anthropic Claude Code docs |
| `kimi` | `https://api.moonshot.ai/anthropic` | `ANTHROPIC_AUTH_TOKEN` | [Kimi Claude Code guide](https://platform.kimi.ai/docs/guide/claude-code-kimi) |
| `kimi-cn` | `https://api.moonshot.cn/anthropic` | `ANTHROPIC_AUTH_TOKEN` | Same guide (China region); community notes `.cn` vs `.ai` |
| `deepseek` | `https://api.deepseek.com/anthropic` | `ANTHROPIC_AUTH_TOKEN` | [DeepSeek Claude Code integration](https://api-docs.deepseek.com/quick_start/agent_integrations/claude_code/) |
| `zhipu` | `https://open.bigmodel.cn/api/anthropic` | `ANTHROPIC_AUTH_TOKEN` | [智谱 Claude API 兼容](https://docs.bigmodel.cn/cn/guide/develop/claude/introduction) |
| `zai` | `https://api.z.ai/api/anthropic` | `ANTHROPIC_AUTH_TOKEN` | [Z.AI Claude Code](https://docs.z.ai/devpack/tool/claude) |
| `qwen` | `https://dashscope.aliyuncs.com/apps/anthropic` | `ANTHROPIC_AUTH_TOKEN` | [Alibaba Model Studio Claude Code](https://www.alibabacloud.com/help/en/model-studio/claude-code) (Beijing pay-as-you-go) |
| `qwen-coding` | `https://coding.dashscope.aliyuncs.com/apps/anthropic` | `ANTHROPIC_AUTH_TOKEN` | Same page (Coding Plan CN) |
| `qwen-coding-intl` | `https://coding-intl.dashscope.aliyuncs.com/apps/anthropic` | `ANTHROPIC_AUTH_TOKEN` | Same page (Coding Plan international) |
| `magpie` | `http://127.0.0.1:3425` | `ANTHROPIC_AUTH_TOKEN` | [magpie integrate](https://usemagpie.ai/docs/integrate) — Anthropic Messages at `/v1/messages` |

Default model ids in presets are taken from the same pages at the time of writing (2026-10-16)
and may change; override via `providers.yaml` or task settings.

## Custom providers

`~/.tidebot/providers.yaml` example:

```yaml
providers:
  - name: my-relay
    base_url: https://relay.example.com
    auth_env: ANTHROPIC_AUTH_TOKEN
    secret_env: TIDEBOT_API_KEY_MY_RELAY
    model: some-model
    opus_model: some-model
    sonnet_model: some-model
    haiku_model: some-model
```

Or start from a preset and override fields:

```yaml
providers:
  - name: kimi-work
    preset: kimi
    model: kimi-k3[1m]
```

## Secrets

- Never store API keys in SQLite plaintext.
- Resolution order: `TIDEBOT_SECRET_PROVIDER_<NAME>` → profile `secret_env` → `TIDEBOT_API_KEY` → `ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_API_KEY`.
- Keys are injected only into the child process env; logs use masking.

## Not verified / caveats

- Exact default model names for Zhipu/Z.AI/Qwen change frequently; treat preset model fields as examples.
- Magpie’s gateway also speaks OpenAI/Gemini; tidebot only uses the Anthropic Messages face.
- We do **not** rewrite `~/.claude/settings.json` (unlike magpie’s agent adapters).
- Anthropic terms: do not share or proxy another user’s Claude credentials; each user brings their own key.
