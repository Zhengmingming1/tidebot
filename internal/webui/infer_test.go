package webui

import (
	"net/http"
	"testing"
)

func TestInferTask(t *testing.T) {
	cases := []struct{ in, name, cron string }{
		{"每天早上 9 点汇总昨天的 GitHub 提交，写成日报发到企业微信", "汇总昨天的 GitHub 提交", "0 9 * * *"},
		{"请每周一上午10点半整理上周的 issue", "整理上周的 issue", "30 10 * * 1"},
		{"工作日下午 6 点备份 workspace 到 S3", "备份 workspace 到 S3", "0 18 * * 1-5"},
		{"每个工作日早上 9 点半汇总昨天的 GitHub 提交，写成日报发到企业微信", "汇总昨天的 GitHub 提交", "30 9 * * 1-5"},
		{"每周一到周五 18:00 提醒我写周报", "提醒我写周报", "0 18 * * 1-5"},
		{"每晚清理 7 天前的日志", "清理 7 天前的日志", "0 21 * * *"},
		{"每 15 分钟检查一下网站是否可访问", "检查一下网站是否可访问", "*/15 * * * *"},
		{"每月1号生成上个月的花费报告", "生成上个月的花费报告", "0 9 1 * *"},
		{"每小时同步一次 RSS", "同步一次 RSS", "0 * * * *"},
		{"Every Monday at 9:30am, summarize the merged PRs and post them to Slack", "Summarize the merged PRs and post", "30 9 * * 1"},
		{"Please back up the database daily at 11pm.", "Back up the database", "0 23 * * *"},
		{"Weekdays at 08:15 check the build status", "Check the build status", "15 8 * * 1-5"},
		{"9 点给我讲个笑话", "给我讲个笑话", ""}, // a time without a frequency: no schedule
		{"Summarize today's commits", "Summarize today's commits", ""},
		{"Every weekday at 9am, summarize yesterday's GitHub commits into a daily report", "Summarize yesterday's GitHub commits", "0 9 * * 1-5"},
		{"", "", ""},
	}
	for _, c := range cases {
		g := InferTask(c.in)
		if g.Name != c.name || g.Cron != c.cron {
			t.Errorf("%q → name %q cron %q; want %q %q", c.in, g.Name, g.Cron, c.name, c.cron)
		}
	}
}

func TestInferEndpoints(t *testing.T) {
	c := login(t)
	code, out := c.json(http.MethodPost, "/api/tasks/infer", map[string]any{"prompt": "每天 8 点汇总新闻"})
	if code != 200 || out["cron"] != "0 8 * * *" || out["name"] != "汇总新闻" {
		t.Fatalf("task infer %d %v", code, out)
	}
	if code, _ := c.json(http.MethodGet, "/api/tasks/infer", nil); code != 405 {
		t.Fatalf("GET %d", code)
	}
	if rr := c.do(http.MethodPost, "/api/tasks/infer", "application/json", []byte("{")); rr.Code != 400 {
		t.Fatalf("bad json %d", rr.Code)
	}
}
