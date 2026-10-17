package provider

import "github.com/Zhengmingming1/tidebot/internal/pricing"

// Official prices for built-in presets. Every number here was read from the
// vendor's own pricing page on the date in Fetched; anything we couldn't verify
// is left nil and shows as "未设置价格" until the user fills it in.
// Prices are per 1M tokens. CacheWrite is the 5-minute cache write where a
// vendor distinguishes TTLs; where a vendor has no separate write charge the
// write is billed as normal input, so CacheWrite = Input.

const fetched = "2026-10-17"

const (
	srcAnthropic = "https://docs.anthropic.com/en/docs/about-claude/pricing"
	srcKimi      = "https://platform.kimi.ai/docs/pricing/chat"
	srcKimiCN    = "https://platform.moonshot.cn/docs/pricing/chat"
	srcDeepSeek  = "https://api-docs.deepseek.com/quick_start/pricing"
	srcZhipu     = "https://docs.bigmodel.cn/cn/guide/start/pricing"
	srcZAI       = "https://docs.z.ai/guides/overview/pricing"
	srcQwen      = "https://help.aliyun.com/zh/model-studio/model-pricing"
)

var f = pricing.F

func mp(model, cur string, in, out, cw, cr *float64, src, note string) pricing.ModelPrice {
	return pricing.ModelPrice{Model: model, Currency: cur, Input: in, Output: out, CacheWrite: cw, CacheRead: cr, Source: src, Fetched: fetched, Note: note}
}

func anthropicPrices() []pricing.ModelPrice {
	a := func(model string, in, out, cw, cr float64) pricing.ModelPrice {
		return mp(model, pricing.USD, f(in), f(out), f(cw), f(cr), srcAnthropic, "")
	}
	haiku55 := a("claude-haiku-5-5*", 0.10, 0.50, 0.125, 0.01)
	haiku55.Tiers = []pricing.Tier{
		{UpTo: 100_001, Input: f(0.10), Output: f(0.50), CacheWrite: f(0.125), CacheRead: f(0.01)},
		{UpTo: 0, Input: f(0.50), Output: f(2.50), CacheWrite: f(0.625), CacheRead: f(0.05)},
	}
	haiku55.Note = "prompts over 100,000 tokens use the higher band"
	return []pricing.ModelPrice{
		a("claude-fable-5-1*", 10, 50, 12.50, 0.25),
		a("claude-fable-5*", 10, 50, 12.50, 1),
		a("claude-opus-5-5*", 4, 20, 5, 0.20),
		a("claude-opus-5*", 5, 25, 6.25, 0.50),
		a("claude-opus-4-8*", 5, 25, 6.25, 0.50),
		a("claude-opus-4-7*", 5, 25, 6.25, 0.50),
		a("claude-opus-4-6*", 5, 25, 6.25, 0.50),
		a("claude-opus-4-5*", 5, 25, 6.25, 0.50),
		a("claude-sonnet-5-5*", 2, 10, 2.50, 0.10),
		a("claude-sonnet-5*", 2, 10, 2.50, 0.20),
		a("claude-sonnet-4-6*", 3, 15, 3.75, 0.30),
		a("claude-sonnet-4-5*", 3, 15, 3.75, 0.30),
		haiku55,
		a("claude-haiku-4-5*", 1, 5, 1.25, 0.10),
	}
}

func kimiPrices(cn bool) []pricing.ModelPrice {
	if cn {
		return []pricing.ModelPrice{
			mp("kimi-k3", pricing.CNY, f(20), f(100), f(20), f(2), srcKimiCN, ""),
			mp("kimi-k2.7-code", pricing.CNY, f(6.5), f(27), f(6.5), f(1.30), srcKimiCN, ""),
			mp("kimi-k2.7-code-highspeed", pricing.CNY, f(13), f(54), f(13), f(2.60), srcKimiCN, ""),
			mp("kimi-k2.6", pricing.CNY, f(6.5), f(27), f(6.5), f(1.10), srcKimiCN, ""),
		}
	}
	return []pricing.ModelPrice{
		mp("kimi-k3", pricing.USD, f(3), f(15), f(3), f(0.30), srcKimi, ""),
		mp("kimi-k2.7-code", pricing.USD, f(0.95), f(4), f(0.95), f(0.19), srcKimi, ""),
		mp("kimi-k2.7-code-highspeed", pricing.USD, f(1.90), f(8), f(1.90), f(0.38), srcKimi, ""),
		mp("kimi-k2.6", pricing.USD, f(0.95), f(4), f(0.95), f(0.16), srcKimi, ""),
	}
}

func deepseekPrices() []pricing.ModelPrice {
	const note = "peak price; off-peak (outside 01:00–04:00 and 06:00–10:00 UTC on weekdays) is half"
	return []pricing.ModelPrice{
		mp("deepseek-flash", pricing.USD, f(0.30), f(1.20), f(0.30), f(0.006), srcDeepSeek, note),
		mp("deepseek-v4-pro", pricing.USD, f(1.32), f(3.96), f(1.32), f(0.044), srcDeepSeek, note),
	}
}

func zhipuPrices() []pricing.ModelPrice {
	glm51 := mp("glm-5.1", pricing.CNY, f(6), f(24), f(6), f(1.3), srcZhipu, "")
	glm51.Tiers = []pricing.Tier{
		{UpTo: 32_000, Input: f(6), Output: f(24), CacheWrite: f(6), CacheRead: f(1.3)},
		{UpTo: 0, Input: f(8), Output: f(28), CacheWrite: f(8), CacheRead: f(2)},
	}
	return []pricing.ModelPrice{
		mp("glm-5.3", pricing.CNY, f(8), f(28), f(8), f(2), srcZhipu, ""),
		mp("glm-5.3-flash", pricing.CNY, f(0.8), f(2.8), f(0.8), f(0.23), srcZhipu, ""),
		mp("glm-5.3-flashx", pricing.CNY, f(2), f(7), f(2), f(0.57), srcZhipu, ""),
		mp("glm-5.2", pricing.CNY, f(8), f(28), f(8), f(2), srcZhipu, ""),
		glm51,
	}
}

func zaiPrices() []pricing.ModelPrice {
	return []pricing.ModelPrice{
		mp("glm-5.3", pricing.USD, f(1.4), f(4.4), f(1.4), f(0.26), srcZAI, ""),
		mp("glm-5.3-flash", pricing.USD, f(0.15), f(0.50), f(0.15), f(0.03), srcZAI, ""),
		mp("glm-5.3-flashx", pricing.USD, f(0.37), f(1.25), f(0.37), f(0.075), srcZAI, ""),
		mp("glm-5.2", pricing.USD, f(1.4), f(4.4), f(1.4), f(0.26), srcZAI, ""),
	}
}

func qwenPrices() []pricing.ModelPrice {
	q := mp("qwen3.7-plus", pricing.CNY, f(2), f(8), nil, nil, srcQwen,
		"list price (a limited-time 20% discount is not applied); context-cache prices not verified")
	q.Tiers = []pricing.Tier{
		{UpTo: 256_001, Input: f(2), Output: f(8)},
		{UpTo: 0, Input: f(6), Output: f(24)},
	}
	return []pricing.ModelPrice{q}
}

var subscription = &pricing.Billing{Mode: pricing.ModeSubscription}
