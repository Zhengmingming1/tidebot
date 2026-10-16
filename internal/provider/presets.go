// Package provider defines model-provider profiles for Claude Code (ANTHROPIC_* env).
// Preset base URLs are taken from official vendor docs — see docs/providers.md.
package provider

// Preset is a built-in, verified Anthropic-compatible provider profile.
type Preset struct {
	ID             string
	DisplayName    string
	BaseURL        string // empty = Anthropic default (do not set ANTHROPIC_BASE_URL)
	AuthEnv        string // ANTHROPIC_AUTH_TOKEN or ANTHROPIC_API_KEY
	DefaultModel   string
	OpusModel      string
	SonnetModel    string
	HaikuModel     string
	FableModel     string
	SmallFastModel string
	SubagentModel  string
	Notes          string
	SourceURL      string
}

// BuiltIn returns verified presets. Base URLs must match official documentation.
func BuiltIn() []Preset {
	return []Preset{
		{
			ID: "anthropic", DisplayName: "Anthropic (official)",
			BaseURL: "", AuthEnv: "ANTHROPIC_API_KEY",
			Notes:     "Official Anthropic API. Leave base URL empty so Claude Code uses its default endpoint.",
			SourceURL: "https://docs.anthropic.com/en/docs/claude-code",
		},
		{
			ID: "kimi", DisplayName: "Kimi / Moonshot (global)",
			BaseURL: "https://api.moonshot.ai/anthropic", AuthEnv: "ANTHROPIC_AUTH_TOKEN",
			DefaultModel: "kimi-k3[1m]", OpusModel: "kimi-k3[1m]", SonnetModel: "kimi-k3[1m]",
			HaikuModel: "kimi-k2.7-code", FableModel: "kimi-k3[1m]", SubagentModel: "kimi-k3[1m]",
			Notes:     "Moonshot Open Platform Anthropic Messages API (global).",
			SourceURL: "https://platform.kimi.ai/docs/guide/claude-code-kimi",
		},
		{
			ID: "kimi-cn", DisplayName: "Kimi / Moonshot (China)",
			BaseURL: "https://api.moonshot.cn/anthropic", AuthEnv: "ANTHROPIC_AUTH_TOKEN",
			DefaultModel: "kimi-k3[1m]", OpusModel: "kimi-k3[1m]", SonnetModel: "kimi-k3[1m]",
			HaikuModel: "kimi-k2.7-code", FableModel: "kimi-k3[1m]", SubagentModel: "kimi-k3[1m]",
			Notes:     "Mainland China endpoint; key must be created on the .cn platform.",
			SourceURL: "https://platform.kimi.ai/docs/guide/claude-code-kimi",
		},
		{
			ID: "deepseek", DisplayName: "DeepSeek",
			BaseURL: "https://api.deepseek.com/anthropic", AuthEnv: "ANTHROPIC_AUTH_TOKEN",
			DefaultModel: "deepseek-flash[1m]", OpusModel: "deepseek-flash[1m]", SonnetModel: "deepseek-flash[1m]",
			HaikuModel: "deepseek-flash", SubagentModel: "deepseek-flash",
			Notes:     "DeepSeek Anthropic-compatible endpoint. Model ids may change; check official docs.",
			SourceURL: "https://api-docs.deepseek.com/quick_start/agent_integrations/claude_code/",
		},
		{
			ID: "zhipu", DisplayName: "Zhipu GLM (bigmodel.cn)",
			BaseURL: "https://open.bigmodel.cn/api/anthropic", AuthEnv: "ANTHROPIC_AUTH_TOKEN",
			DefaultModel: "glm-5.3", OpusModel: "glm-5.3", SonnetModel: "glm-5.3", HaikuModel: "glm-5.3",
			Notes:     "Zhipu Open Platform Claude API compatibility (China).",
			SourceURL: "https://docs.bigmodel.cn/cn/guide/develop/claude/introduction",
		},
		{
			ID: "zai", DisplayName: "Z.AI GLM (international)",
			BaseURL: "https://api.z.ai/api/anthropic", AuthEnv: "ANTHROPIC_AUTH_TOKEN",
			DefaultModel: "glm-5.3", OpusModel: "glm-5.3", SonnetModel: "glm-5.3",
			HaikuModel: "glm-5.3-flash",
			Notes:     "Z.AI international Anthropic-compatible endpoint for Claude Code.",
			SourceURL: "https://docs.z.ai/devpack/tool/claude",
		},
		{
			ID: "qwen", DisplayName: "Qwen / DashScope (Beijing pay-as-you-go)",
			BaseURL: "https://dashscope.aliyuncs.com/apps/anthropic", AuthEnv: "ANTHROPIC_AUTH_TOKEN",
			DefaultModel: "qwen3.7-plus", OpusModel: "qwen3.7-plus", SonnetModel: "qwen3.7-plus",
			HaikuModel: "qwen3.7-plus", SubagentModel: "qwen3.7-plus",
			Notes:     "Alibaba Cloud Model Studio Anthropic-compatible Messages API (Beijing).",
			SourceURL: "https://www.alibabacloud.com/help/en/model-studio/claude-code",
		},
		{
			ID: "qwen-coding", DisplayName: "Qwen / DashScope Coding Plan (CN)",
			BaseURL: "https://coding.dashscope.aliyuncs.com/apps/anthropic", AuthEnv: "ANTHROPIC_AUTH_TOKEN",
			DefaultModel: "qwen3.7-plus", OpusModel: "qwen3.7-plus", SonnetModel: "qwen3.7-plus",
			HaikuModel: "qwen3.7-plus", SubagentModel: "qwen3.7-plus",
			Notes:     "Coding Plan dedicated endpoint; requires Coding Plan API key.",
			SourceURL: "https://www.alibabacloud.com/help/en/model-studio/claude-code",
		},
		{
			ID: "qwen-coding-intl", DisplayName: "Qwen / DashScope Coding Plan (intl)",
			BaseURL: "https://coding-intl.dashscope.aliyuncs.com/apps/anthropic", AuthEnv: "ANTHROPIC_AUTH_TOKEN",
			DefaultModel: "qwen3.7-plus", OpusModel: "qwen3.7-plus", SonnetModel: "qwen3.7-plus",
			HaikuModel: "qwen3.7-plus", SubagentModel: "qwen3.7-plus",
			Notes:     "International Coding Plan Anthropic-compatible endpoint.",
			SourceURL: "https://www.alibabacloud.com/help/en/model-studio/claude-code",
		},
		{
			ID: "magpie", DisplayName: "Magpie local gateway",
			BaseURL: "http://127.0.0.1:3425", AuthEnv: "ANTHROPIC_AUTH_TOKEN",
			DefaultModel: "", Notes: "Local magpie gateway (Anthropic Messages at /v1/messages). Token is often the literal 'magpie' when using the local gateway.",
			SourceURL: "https://usemagpie.ai/docs/integrate",
		},
	}
}

// LookupPreset returns a built-in preset by id, or false if unknown.
func LookupPreset(id string) (Preset, bool) {
	for _, p := range BuiltIn() {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
