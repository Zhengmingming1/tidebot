package webui

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestLiveRunUsage: a running run's cost/token cards refresh in place from the usage the
// core saves mid-run (no remount), stop once it ends, and their text is escaped.
func TestLiveRunUsage(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n") // Windows checkouts may use CRLF
	for _, want := range []string{
		`'<div class="card stat" id="rd-cost">' + runCostStat(r, live) + "</div>"`,
		`'<div class="card stat" id="rd-tok">' + runTokStat(r, live) + "</div></div>"`,
		`if (live) livePoll(function () { return liveRunStats(id); }, RUN_STATS_MS);`,
		`if (prev.c !== next.c) c.innerHTML = next.c;`,
		`if (!still) stopPoll();`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	for _, lang := range []string{"zh-CN", "en"} {
		raw, err := os.ReadFile("static/i18n/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"rd.tokens", "rd.cost_so_far", "rd.tokens_so_far", "rd.usage_live_tip"} {
			if m[k] == "" {
				t.Errorf("%s: missing %s", lang, k)
			}
		}
	}
	cost := regexp.MustCompile(`(?s)function runCostStat\(r, live\) \{.*?\n  \}\n`).FindString(src)
	tok := regexp.MustCompile(`(?s)function runTokStat\(r, live\) \{.*?\n  \}\n`).FindString(src)
	if cost == "" || tok == "" {
		t.Fatal("stat card helpers not found")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `function esc(s){return String(s).replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/"/g,"&quot;");}
var state={currency:"USD"};
function T(k,v){return k+(v?":"+JSON.stringify(v):"");}
function money(a,c){return c+a;} function usd(v){return "$"+v;} function num(n){return String(n);}
function costBadge(){return "";}
` + cost + tok + `
var r={cost_usd:0.12,cost_amount:0.8,cost_currency:"CNY",cost_source:"priced",bucket:"<b>x",input_tokens:2000,output_tokens:300,cache_read_tokens:0,cache_write_tokens:0};
var live=runCostStat(r,true), done=runCostStat(r,false), tk=runTokStat(r,true);
var bad=[];
if (live.indexOf("rd.cost_so_far")<0 || live.indexOf('data-tip="rd.usage_live_tip"')<0) bad.push("live cost: "+live);
if (done.indexOf("col.cost")<0 || done.indexOf("data-tip")>=0) bad.push("finished cost: "+done);
if (tk.indexOf("rd.tokens_so_far")<0 || tk.indexOf("<strong>2300</strong>")<0) bad.push("tokens: "+tk);
r.cost_currency="USD"; if (runCostStat(r,false).indexOf("<b>x")>=0) bad.push("bucket not escaped");
if (bad.length){console.error(bad.join("\n"));process.exitCode=1;}
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("stat cards: %v\n%s", err, out)
	}
}
