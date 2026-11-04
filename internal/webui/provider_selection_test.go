package webui

import (
	"context"
	"net/http"
	"os/exec"
	"testing"
)

type selectionProviderAdmin struct{ ProviderAdmin }

func (selectionProviderAdmin) ListProviders(context.Context) []ProviderView {
	return []ProviderView{
		{Name: "unconfigured", AuthEnv: "ANTHROPIC_API_KEY"},
		{Name: "configured", AuthEnv: "ANTHROPIC_API_KEY", KeySet: true},
		// The managed gateway supplies its own token, without a user-entered key.
		{Name: "free-pool", ManagedBy: "kilo-free-pool", KeySet: true},
	}
}

func TestSettingsPreservesProviderReadiness(t *testing.T) {
	c := login(t)
	c.s.deps.Providers = selectionProviderAdmin{}
	code, data := c.json(http.MethodGet, "/api/settings", nil)
	if code != http.StatusOK {
		t.Fatalf("settings: %d %v", code, data)
	}
	ps := data["providers"].([]any)
	if len(ps) != 3 {
		t.Fatalf("all providers must remain in settings DTO: %v", ps)
	}
	for i, want := range []bool{false, true, true} {
		if got := ps[i].(map[string]any)["key_set"]; got != want {
			t.Errorf("provider %d key_set=%v want %v", i, got, want)
		}
	}
}

func TestProviderSelectionClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	script := `
const assert=require('node:assert/strict'), fs=require('node:fs');
const src=fs.readFileSync('src/task-form.js','utf8');
function grab(name) { const m=src.match(new RegExp('  function '+name+'\\([^]*?\\n  \\}')); assert.ok(m,name);return m[0]; }
function esc(v) { return String(v == null?'':v).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
function T(k) {return k;}
function opt(v,label,selected) {return '<option value="'+esc(v)+'"'+(selected?' selected':'')+'>'+esc(label)+'</option>';}
eval(grab('readyProviders')+'\n'+grab('smartProvider')+'\n'+grab('providerOptions'));
const st={default_provider:'missing',providers:[
 {name:'missing',key_set:false,model:'paid'},
 {name:'unknown',auth_env:''},
 {name:'paid',key_set:true,model:'model-1'},
 {name:'free-pool',key_set:true,managed_by:'kilo-free-pool'}
]};
assert.deepEqual(readyProviders(st).map(p=>p.name),['paid','free-pool']);
assert.equal(smartProvider(st),'paid');
assert.equal(smartProvider({...st,default_provider:'free-pool'}),'free-pool');
assert.equal(smartProvider({providers:[]}), '');
let html=providerOptions(st,'missing',false,true);
assert.ok(!html.includes('value="missing"'));
assert.ok(!html.includes('value="unknown"'));
assert.ok(html.includes('value="paid" selected'));
assert.ok(html.includes('paid · model-1'));
assert.ok(html.includes('value="free-pool"'));
html=providerOptions(st,'deleted"<>',true,true);
assert.ok(html.startsWith('<option value="deleted&quot;&lt;&gt;" selected disabled hidden>'));
assert.ok(!html.includes('value="paid" selected'), 'never substitute an explicit unavailable selection');
html=providerOptions(st,'missing',true,false);
assert.ok(html.includes('value="missing" selected disabled hidden'));
assert.ok(html.includes('qr.no_key'));
html=providerOptions(st,'',true,false);
assert.ok(html.includes('value="" selected disabled hidden'), 'preserve inherited/default task selection');
assert.ok(providerOptions({providers:[]},'',false,false).includes('cc.no_provider'));
assert.equal(st.providers.length,4,'filtering must not mutate settings');
for (const [file,needle] of [['home.js','providerOptions(st, chosen'],['home.js','providerOptions(st, lastProv'],['task-form.js','providerOptions(st, tf.provider'],['compositions-workflows.js','providerOptions(settings, smartProvider'],['compositions-workflows.js','providerOptions(st, smartProvider']]) {
 assert.ok(fs.readFileSync('src/'+file,'utf8').includes(needle),file+' uses shared filter');
}
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("provider selection: %v\n%s", err, out)
	}
}
