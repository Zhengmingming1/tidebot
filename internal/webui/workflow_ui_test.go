package webui

import (
	"strings"
	"testing"
)

// The editor offers the folder, the three levels and an opt-in total budget; a saved
// workflow without a level stays read-only; the schedule button opens the task form
// with the workflow pinned to the saved version.
func TestWorkflowAlignmentUI(t *testing.T) {
	b := []byte(appJSSource(t))
	src := string(b)
	for _, want := range []string{
		`run_mode:'exec',workspace_dir:''`,
		`if(!def.run_mode)def.run_mode='read';`,
		`modeSegHtml('run_mode',def.run_mode,st.sandbox)`,
		`i.disabled=!dir&&i.value==='edit';`,
		`var bv=f.budget_enabled.checked?+f.budget.value||0:0;`,
		`taskFormModal(null, { name: w.name, workflow: { id: w.id, version: w.version, current: w.version, name: w.name } });`,
		`error(T('wf.save_first_sched'));return;}wfSchedule(saved);`,
		`execFirstUse(st.sandbox,`,
		`body.workflow = {`,
		`workflow_current: "tx.n_wf_current"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}
