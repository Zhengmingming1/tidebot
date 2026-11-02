package webui

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// Execute the shipped layout/render helpers, then parse their SVG as XML rather
// than accepting escaped-looking substrings. Real pointer interaction is covered
// separately by the browser smoke test.
func TestWorkflowCanvasTopologyAndSafeSVG(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	src := liveSrc(t)
	script := regexpFunc(t, src, "  function esc(v) {") + regexpFunc(t, src, "  function wfLevels(nodes) {") + regexpFunc(t, src, "  function wfArrange(def) {") + regexpFunc(t, src, "  function wfScene(def, selected, states) {") + `
const assert=require('node:assert/strict'),fs=require('node:fs');
const copy=x=>JSON.parse(JSON.stringify(x));
const graph=[
 {id:'root',name:'根节点',kind:'skill',needs:[]},
 {id:'left',name:'左侧',kind:'skill',needs:[{node_id:'root'}]},
 {id:'right',name:'右侧',kind:'skill',needs:[{node_id:'root'}]},
 {id:'merge',name:'汇合',kind:'skill',needs:[{node_id:'left'},{node_id:'right'}]},
 {id:'end',name:'完成',kind:'skill',needs:[{node_id:'merge'}]}
];
const expected={root:0,left:1,right:1,merge:2,end:3};
function permutations(a){if(!a.length)return [[]];return a.flatMap((x,i)=>permutations(a.filter((_,j)=>i!==j)).map(p=>[x,...p]));}
for(const permutation of permutations(graph)){
 const levels=wfLevels(permutation);
 assert.ok(levels,'valid graph rejected');assert.deepEqual({...levels},expected,'array ordering must not determine topology');
}
assert.equal(wfLevels([{id:'a',needs:[{node_id:'b'}]},{id:'b',needs:[{node_id:'a'}]}]),null);
assert.equal(wfLevels([{id:'a',needs:[{node_id:'a'}]}]),null);
assert.equal(wfLevels([{id:'a',needs:[{node_id:'missing'}]}]),null);
// These IDs are legal on the backend; inherited Object properties are not nodes.
assert.equal(wfLevels([{id:'a',needs:[{node_id:'constructor'}]}]),null);
assert.equal(wfLevels([{id:'constructor',needs:[{node_id:'a'}]},{id:'a',needs:[{node_id:'constructor'}]}]),null);
assert.deepEqual({...wfLevels([{id:'toString',needs:[{node_id:'constructor'}]},{id:'constructor',needs:[]}])},{constructor:0,toString:1});

const def={nodes:copy(graph)};def.nodes.reverse();
const withoutPositions=d=>d.nodes.map(({position,...n})=>n);
const before=copy(withoutPositions(def));assert.equal(wfArrange(def),true);
assert.deepEqual(withoutPositions(def),before,'arranging must not rewrite dependencies or metadata');
const positions=Object.fromEntries(def.nodes.map(n=>[n.id,n.position]));
assert.equal(positions.left.x,positions.right.x,'parallel branches belong in one layer');
assert.notEqual(positions.left.y,positions.right.y,'parallel branches must not overlap');
assert.ok(positions.root.x<positions.left.x&&positions.left.x<positions.merge.x&&positions.merge.x<positions.end.x);
for(const n of def.nodes)n.position={x:1000-n.position.x,y:n.position.y+90};
assert.deepEqual({...wfLevels(def.nodes)},expected,'moving a box must never change execution order');
const invalid={nodes:[{id:'a',needs:[{node_id:'b'}],position:{x:1,y:2}}]},invalidBefore=copy(invalid);
assert.equal(wfArrange(invalid),false);assert.deepEqual(invalid,invalidBefore,'failed arrange must leave the draft intact');

let dict={};function T(key){return dict[key]||key;}
const scenes=[];
function scene(name,d,selected,states){const before=copy(d),r=wfScene(d,selected,states);assert.deepEqual(d,before,'drawing must not mutate the graph');assert.ok(Number.isFinite(r.width)&&r.width>0&&Number.isFinite(r.height)&&r.height>0);scenes.push({name,...r});}
scene('parallel',def,'merge',{});
const hostileID='node"><script>alert(1)</script>',hostileName='"><img src=x onerror="attack()"> & 潮汐🙂';
dict={'wf.skill_step':'<svg onload="attack()">','wf.branch_true':'"><script>branch()</script>'};
scene('hostile',{nodes:[{id:hostileID,name:hostileName,kind:'skill',needs:[],position:{x:10,y:20}}]},hostileID,{});
scene('hostile-status',{nodes:[{id:'safe',name:'safe',kind:'skill',needs:[],position:{x:10,y:20}}]},'',{safe:'" onload="attack()'});
const branch={nodes:[{id:'gate',name:'选择',kind:'condition',needs:[],position:{x:20,y:50}},{id:'yes',name:'真分支',kind:'skill',needs:[{node_id:'gate',branch:'true'}],position:{x:300,y:30}},{id:'no',name:'假分支',kind:'skill',needs:[{node_id:'gate',branch:'false'}],position:{x:300,y:180}}]};
scene('branch',branch,'',{});
const states=['pending','queued','running','succeeded','failed','canceled','skipped','interrupted'];
const labels={};
for(const locale of ['zh-CN','en']){
 dict=JSON.parse(fs.readFileSync('static/i18n/'+locale+'.json','utf8'));
 for(const state of states){const key='wf.status_'+state;assert.ok(dict[key],locale+' missing status '+state);labels[locale+':'+state]=dict[key];scene(locale+':'+state,{nodes:[{id:'node',name:'node',kind:'skill',needs:[],position:{x:1,y:2}}]},'',{node:state});}
}
process.stdout.write(JSON.stringify({scenes,hostileID,hostileName,labels}));
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("workflow canvas logic: %v\n%s", err, out)
	}
	var result struct {
		Scenes []struct {
			Name, HTML    string
			Width, Height float64
		}
		HostileID, HostileName string
		Labels                 map[string]string
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("canvas results: %v\n%s", err, out)
	}
	for _, scene := range result.Scenes {
		t.Run(scene.Name, func(t *testing.T) {
			root := parseWorkflowSVG(t, scene.HTML)
			switch scene.Name {
			case "parallel":
				if len(root.Groups) != 5 || len(root.Paths) != 5 {
					t.Fatalf("nodes or edges lost: %+v", root)
				}
				selected := 0
				for _, n := range root.Groups {
					if strings.Contains(" "+n.Class+" ", " selected ") {
						selected++
						if n.ID != "merge" {
							t.Fatal("selected the wrong node")
						}
					}
				}
				if selected != 1 {
					t.Fatal("selection is not unique")
				}
			case "hostile":
				if len(root.Groups) != 1 {
					t.Fatal("untrusted label created extra nodes")
				}
				n := root.Groups[0]
				if n.ID != result.HostileID || n.Label != result.HostileName || len(n.Texts) != 3 || n.Texts[1] != result.HostileID || n.Texts[2] != `<svg onload="attack()">` {
					t.Fatalf("untrusted values did not remain plain SVG data: %+v", n)
				}
			case "hostile-status":
				if len(root.Groups) != 1 || root.Groups[0].Texts[2] != `wf.status_" onload="attack()` {
					t.Fatalf("status was not plain text: %+v", root)
				}
			case "branch":
				if len(root.Groups) != 3 || len(root.Paths) != 2 || len(root.Texts) != 2 {
					t.Fatalf("branch edges lost: %+v", root)
				}
				gate := root.Groups[0]
				branches := map[string]bool{}
				for _, c := range gate.Circles {
					if c.Out != "" {
						branches[c.Branch] = true
					}
				}
				if len(branches) != 2 || !branches["true"] || !branches["false"] {
					t.Fatalf("condition ports: %v", branches)
				}
			default:
				parts := strings.Split(scene.Name, ":")
				state := parts[1]
				if len(root.Groups) != 1 {
					t.Fatalf("status scene: %+v", root)
				}
				n := root.Groups[0]
				if !strings.Contains(" "+n.Class+" ", " "+state+" ") || len(n.Texts) != 3 || n.Texts[2] != result.Labels[scene.Name] {
					t.Fatalf("wrong status rendering: %+v", n)
				}
			}
		})
	}
}

type workflowSVG struct {
	Groups []struct {
		Class   string   `xml:"class,attr"`
		ID      string   `xml:"data-node,attr"`
		Label   string   `xml:"aria-label,attr"`
		Texts   []string `xml:"text"`
		Circles []struct {
			Out    string `xml:"data-out,attr"`
			Branch string `xml:"data-branch,attr"`
		} `xml:"circle"`
	} `xml:"g"`
	Paths []struct{} `xml:"path"`
	Texts []string   `xml:"text"`
}

func parseWorkflowSVG(t *testing.T, html string) workflowSVG {
	t.Helper()
	data := []byte("<svg>" + html + "</svg>")
	allowed := map[string]string{
		"svg": "", "g": " class data-node tabindex role aria-label transform ",
		"path": " class d ", "rect": " class width height rx ", "text": " class x y ",
		"circle": " class data-in data-out data-branch cx cy r ",
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid SVG: %v", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		attrs, ok := allowed[start.Name.Local]
		if !ok {
			t.Fatalf("unexpected executable/foreign SVG element %s", start.Name.Local)
		}
		for _, a := range start.Attr {
			if a.Name.Space != "" || !strings.Contains(attrs, " "+a.Name.Local+" ") {
				t.Fatalf("injected SVG attribute %s on %s", a.Name.Local, start.Name.Local)
			}
		}
	}
	var root workflowSVG
	if err := xml.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	return root
}
