package webui

import (
	"strings"
	"testing"
)

// A workflow task's edit form keeps 检查依赖 and checks the workflow version
// picked in the form (with the typed input); the API accepts that version only
// for a saved workflow.
func TestTaskFormWorkflowDependencyCheck(t *testing.T) {
	src := appJSSource(t)
	for _, want := range []string{
		`<button type="button" class="btn" id="tf-dependencies">`,
		`if(wf){dependencyCheck({kind:"workflow",id:wf.id,version:Number(f.wf_version.value)||0,prompt:f.prompt.value});return;}`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	if strings.Contains(src, `(wf ? " hidden" : "") + '" id="tf-dependencies"`) {
		t.Error("检查依赖 is still hidden for workflow tasks")
	}
	for _, tc := range []struct {
		req DependencyCheckRequest
		ok  bool
	}{
		{DependencyCheckRequest{Kind: "workflow", ID: "wf1", Version: 2, Prompt: "x"}, true},
		{DependencyCheckRequest{Kind: "workflow", ID: "wf1"}, true},
		{DependencyCheckRequest{Kind: "workflow", ID: "wf1", Version: -1}, false},
		{DependencyCheckRequest{Kind: "task", ID: "t1", Version: 1}, false},
		{DependencyCheckRequest{Kind: "draft", Prompt: "x", Version: 1}, false},
	} {
		if err := tc.req.Validate(); (err == nil) != tc.ok {
			t.Errorf("%+v: err %v", tc.req, err)
		}
	}
}
