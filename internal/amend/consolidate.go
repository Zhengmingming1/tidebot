package amend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// Consolidation thresholds (final-plan §6.4, suggested values).
const (
	ConsolidateAfterItems = 15
	ConsolidateAfterDays  = 30
)

// ConsolidationChange is one item-level edit (never a wholesale rewrite).
type ConsolidationChange struct {
	Op      string   `json:"op"`      // remove_duplicate | merge_overlap
	Keep    string   `json:"keep"`    // constraint id kept
	Remove  []string `json:"remove"`  // constraint ids removed
	Text    string   `json:"text"`    // resulting text of the kept constraint
	Sources []string `json:"sources"` // provenance carried over
}

// ConsolidationPlan is shown as a before/after diff and applied only on confirmation.
type ConsolidationPlan struct {
	TaskID  string                   `json:"task_id"`
	Before  []tasksvc.SpecConstraint `json:"before"`
	After   []tasksvc.SpecConstraint `json:"after"`
	Changes []ConsolidationChange    `json:"changes"`
	Hash    string                   `json:"hash"` // must match on apply (spec unchanged meanwhile)
}

func normText(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func specHash(cs []tasksvc.SpecConstraint) string {
	h := sha256.New()
	for _, c := range cs {
		h.Write([]byte(c.ID + "\x00" + c.Text + "\x00" + c.Source + "\x01"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func joinSources(a, b string) string {
	seen := map[string]bool{}
	var out []string
	for _, s := range strings.Split(a+","+b, ",") {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return strings.Join(out, ",")
}

// PlanConsolidation dedupes constraints and merges ones fully contained in another,
// keeping the earliest position and the union of sources.
func PlanConsolidation(taskID string, cs []tasksvc.SpecConstraint) ConsolidationPlan {
	p := ConsolidationPlan{TaskID: taskID, Before: cs, Hash: specHash(cs)}
	removed := map[int]bool{}
	after := append([]tasksvc.SpecConstraint(nil), cs...)
	for i := range after {
		if removed[i] {
			continue
		}
		ni := normText(after[i].Text)
		for j := i + 1; j < len(after); j++ {
			if removed[j] {
				continue
			}
			nj := normText(after[j].Text)
			if ni == "" || nj == "" {
				continue
			}
			switch {
			case ni == nj:
				removed[j] = true
				after[i].Source = joinSources(after[i].Source, after[j].Source)
				p.Changes = append(p.Changes, ConsolidationChange{Op: "remove_duplicate", Keep: after[i].ID, Remove: []string{after[j].ID}, Text: after[i].Text, Sources: strings.Split(after[i].Source, ",")})
			case strings.Contains(nj, ni) || strings.Contains(ni, nj):
				// keep the more specific (longer) wording at the earlier position
				if len(nj) > len(ni) {
					after[i].Text, ni = after[j].Text, nj
				}
				removed[j] = true
				after[i].Source = joinSources(after[i].Source, after[j].Source)
				p.Changes = append(p.Changes, ConsolidationChange{Op: "merge_overlap", Keep: after[i].ID, Remove: []string{after[j].ID}, Text: after[i].Text, Sources: strings.Split(after[i].Source, ",")})
			}
		}
	}
	for i, c := range after {
		if !removed[i] {
			p.After = append(p.After, c)
		}
	}
	return p
}

func (s *Service) lastConsolidated(ctx context.Context, taskID string) time.Time {
	var v string
	_ = s.DB.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE user_id='owner' AND key=?`, "consolidated_at."+taskID).Scan(&v)
	t, _ := time.Parse(`"`+time.RFC3339+`"`, v)
	return t
}

// ShouldConsolidate applies the §6.4 trigger.
func (s *Service) ShouldConsolidate(ctx context.Context, taskID string, now time.Time) bool {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM requirement_items WHERE task_id=? AND status='applied'`, taskID).Scan(&n)
	if n > ConsolidateAfterItems {
		return true
	}
	last := s.lastConsolidated(ctx, taskID)
	if last.IsZero() {
		var first string
		_ = s.DB.QueryRowContext(ctx, `SELECT MIN(created_at) FROM requirement_items WHERE task_id=? AND status='applied'`, taskID).Scan(&first)
		last, _ = time.Parse(time.RFC3339, first)
	}
	return n > 0 && !last.IsZero() && now.Sub(last) > ConsolidateAfterDays*24*time.Hour
}

// Consolidation returns the plan for the task's current spec.
func (s *Service) Consolidation(ctx context.Context, taskID string) (ConsolidationPlan, error) {
	view, err := s.Tasks.Get(ctx, taskID)
	if err != nil || view.Spec == nil {
		return ConsolidationPlan{}, errors.New("task not found")
	}
	return PlanConsolidation(taskID, view.Spec.Constraints), nil
}

// ApplyConsolidation commits the plan as a new version (rollback = move current back) and
// archives the applied requirement items.
func (s *Service) ApplyConsolidation(ctx context.Context, taskID, hash, actor string) (string, error) {
	view, err := s.Tasks.Get(ctx, taskID)
	if err != nil || view.Spec == nil {
		return "", errors.New("task not found")
	}
	plan := PlanConsolidation(taskID, view.Spec.Constraints)
	if plan.Hash != hash {
		return "", errors.New("the spec changed since the plan was made; review it again")
	}
	if len(plan.Changes) == 0 {
		return "", errors.New("nothing to consolidate")
	}
	spec := *view.Spec
	spec.Constraints = plan.After
	nv, err := s.Tasks.UpdateSpec(ctx, taskID, spec, actor, "consolidate requirements")
	if err != nil {
		return "", err
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE requirement_items SET status='archived' WHERE task_id=? AND status='applied'`, taskID)
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO settings(user_id, key, value_json, updated_at) VALUES('owner',?,?,?)
ON CONFLICT(user_id, key) DO UPDATE SET value_json=excluded.value_json, updated_at=excluded.updated_at`,
		"consolidated_at."+taskID, `"`+time.Now().UTC().Format(time.RFC3339)+`"`, time.Now().UTC().Format(time.RFC3339))
	if nv.CurrentVersion != nil {
		return nv.CurrentVersion.ID, nil
	}
	return "", nil
}
