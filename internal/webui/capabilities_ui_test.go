package webui

import (
	"os"
	"strings"
	"testing"
)

func TestCapabilityUI(t *testing.T) {
	b, err := os.ReadFile("src/capabilities.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, bad := range []string{"window.confirm(", "window.alert(", "window.prompt(", "fetch(", "eval("} {
		if strings.Contains(src, bad) {
			t.Fatalf("unsafe UI construct %s", bad)
		}
	}
	app := appJS(t)
	for _, want := range []string{`case "plugins": p = renderExtensions(parts[1]); break;`, `capRefreshSession(sec, s.id)`, `secondary_confirm:`, `session_id: p.session_id`, `textContent = v.text`, `capAutoTest(v.id)`, `/api/capabilities/storage/gc`, `/credentials_clear`, `reviewed: true`, `default_enabled:value`, `capLoadSkillBadges(page())`, `T("cap.smoke_prompt")`, `(r.cli_auth || [])`, `local_choices:localChoices`, `data-cap-local-choice`, `/api/capabilities/sources`, `/api/capabilities/dependency-preferences`, `providers=skills.sh,github`, `capLoadLegacyInventory(results)`} {
		if !strings.Contains(app, want) {
			t.Errorf("missing capability UI integration %s", want)
		}
	}
	runNode(t, `
var state={dict:{}};
function T(s){return s;}
function esc(s){return String(s == null ? '' : s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;').replace(/'/g,'&#39;');}
function enc(s){return encodeURIComponent(s);}
function ic(s){return '<i>'+s+'</i>';}
`+src+`
var bad=[];function ok(b,m){if(!b)bad.push(m);}
ok(capCanConnect({type:'mcp',mcp:{type:'http',url:'https://example.com/mcp'}}),'registry remotes can connect without an upstream OAuth flag');
ok(!capCanConnect({type:'mcp',disabled:true,mcp:{type:'http'}}),'disabled remote cannot connect');
ok(!capCanConnect({type:'mcp',mcp:{type:'stdio',oauth:true}}),'stdio cannot connect via OAuth');
var l={title:'<img src=x onerror=1>',source:{url:'<script>alert(1)</script>',trust:'community',commit:'deadbeef'},components:[{name:'<b>skill</b>'}],requires:{tools:[{name:'python',version:'>=3.9'}]},scan:{verdict:'caution',findings:[{file:'SKILL.md',line:1,note:'<svg onload=x>'}]}};
var p={id:'p1',session_id:'s1',status:'pending',expires_at:'2099-01-01T00:00:00Z',lock:l,policy:{allowed:true,review_required:true}};
var h=capProposalHtml(p);
ok(!h.includes('<img')&&!h.includes('<script')&&!h.includes('<svg')&&!h.includes('<b>skill'),'untrusted fields escaped');
ok(h.includes('data-cap-confirm disabled')&&h.includes('data-cap-reviewed'),'review gates installation');
ok(h.includes('data-cap-files')&&h.includes('deadbeef'),'source and review available');
p.policy={allowed:false};h=capProposalHtml(p);ok(h.includes('data-cap-confirm disabled'),'blocked source disabled');
p.policy={allowed:true};p.status='applied';h=capProposalHtml(p);ok(!h.includes('data-cap-confirm')&&!h.includes('data-cap-dismiss'),'cannot replay applied card');
p.status='pending';p.expires_at='2000-01-01T00:00:00Z';h=capProposalHtml(p);ok(!h.includes('data-cap-confirm')&&h.includes('expired'),'expired proposal has no install');
p.expires_at='2099-01-01T00:00:00Z';p.policy={allowed:true,second_confirmation_required:true};h=capProposalHtml(p);ok(h.includes('data-cap-risk')&&h.includes('data-cap-confirm disabled'),'secondary gate');
p.kind='uninstall';p.base_version_id='installed-v42';p.policy={allowed:true,review_required:true};p.uninstall_preview={removed_skills:['skill'],retained:[{reason:'<script>bad</script>'}]};h=capProposalHtml(p);ok(h.includes('installed-v42')&&h.includes('cap.uninstall_review')&&!h.includes('data-cap-files')&&!h.includes('<script>'),'uninstall displays pinned escaped deletion summary without file review');
p.kind='update';p.skill_reconciliation=[{skill_id:'sk1',name:'<img>',current_version_id:'local-v1',locally_modified:true}];h=capProposalHtml(p);ok(h.includes('data-cap-local-choice="sk1"')&&h.includes('keep_local')&&h.includes('adopt_upstream')&&!h.includes('<img>'),'local edit review choices are escaped and explicit');
ok(capTabs('tools').includes('aria-current="page"')&&capTabs('tools').includes('#/plugins/plugins'),'all extension tabs');
if(bad.length){console.error(bad.join('\n'));process.exitCode=1;}
`, nil)
}
