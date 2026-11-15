// Package optimize implements skill optimization with three modes (off / propose / canary),
// canary routing, comparison metrics and automatic canary withdrawal (final-plan §7).
package optimize

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

// Trigger thresholds (suggested values).
const (
	MinRunsForAnalysis = 20
	MinDaysForAnalysis = 7
	DeviationThreshold = 0.30
)

// Event kinds emitted through OnEvent.
const (
	EventProposal   = "skill_proposal"
	EventWithdrawn  = "canary_withdrawn"
	EventCanaryDone = "canary_done"
)

// Proposal is a candidate skill version.
type Proposal struct {
	ID        string  `json:"id"`
	SkillID   string  `json:"skill_id"`
	VersionID string  `json:"version_id"`
	Source    string  `json:"source"`
	Category  string  `json:"category"`
	Rationale string  `json:"rationale"`
	Risk      string  `json:"risk"`
	Checks    []Check `json:"checks"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"created_at"`
}

// Service ties skills, versions and run outcomes together.
type Service struct {
	DB      *sql.DB
	Skills  *skillstore.Service
	Ver     *version.Service
	OnEvent func(kind, skillID, detail string)
	mu      sync.Mutex
	rnd     *rand.Rand
}

// New creates the service.
func New(db *sql.DB, sk *skillstore.Service, ver *version.Service) *Service {
	return &Service{DB: db, Skills: sk, Ver: ver, rnd: rand.New(rand.NewSource(time.Now().UnixNano()))}
}

func (s *Service) emit(kind, skillID, detail string) {
	if s.OnEvent != nil {
		s.OnEvent(kind, skillID, detail)
	}
}

// Settings of one skill.
type Settings struct {
	Mode  string  `json:"mode"`
	Ratio float64 `json:"canary_ratio"`
	Runs  int     `json:"canary_runs"`
}

// GetSettings reads a skill's optimization settings.
func (s *Service) GetSettings(ctx context.Context, skillID string) (Settings, error) {
	var st Settings
	err := s.DB.QueryRowContext(ctx, `SELECT optimize_mode, canary_ratio, canary_runs FROM skills WHERE id=?`, skillID).Scan(&st.Mode, &st.Ratio, &st.Runs)
	return st, err
}

// SetSettings updates mode (off|propose|canary), ratio (0..1) and run count.
func (s *Service) SetSettings(ctx context.Context, skillID string, st Settings) error {
	switch st.Mode {
	case "off", "propose", "canary":
	default:
		return errors.New("mode must be off, propose or canary")
	}
	if st.Ratio <= 0 || st.Ratio > 1 {
		st.Ratio = 0.2
	}
	if st.Runs <= 0 {
		st.Runs = 10
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE skills SET optimize_mode=?, canary_ratio=?, canary_runs=? WHERE id=?`, st.Mode, st.Ratio, st.Runs, skillID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("skill not found")
	}
	return nil
}

func (s *Service) skillName(ctx context.Context, skillID string) string {
	var n string
	_ = s.DB.QueryRowContext(ctx, `SELECT name FROM skills WHERE id=?`, skillID).Scan(&n)
	return n
}

// Propose stores files as a candidate version (current untouched), runs static checks and,
// depending on mode and risk, starts a canary or waits for approval.
// source: "auto" (analyzer), "user" (explicit "write this into the skill") or
// "session" (a session's skill draft saved as a version: the owner is on the
// page and adopts it there, so no "new proposal" notice is sent).
func (s *Service) Propose(ctx context.Context, skillID string, files map[string][]byte, source, category, rationale string) (*Proposal, error) {
	cur, err := s.Ver.CurrentVersion(ctx, version.EntitySkill, skillID)
	if err != nil {
		return nil, errors.New("skill has no current version")
	}
	oldFiles, err := s.Skills.Files(ctx, cur.ID)
	if err != nil {
		return nil, err
	}
	// negative feedback: identical content was already rejected → don't propose again
	checks, risk, _ := StaticChecks(oldFiles, files)
	ver, _, err := s.Skills.CommitFiles(ctx, skillID, s.skillName(ctx, skillID), files, "optimization", "optimizer", true)
	if err != nil {
		return nil, err
	}
	if ver.ID == cur.ID {
		return nil, errors.New("proposal is identical to the current version")
	}
	var rejectedBefore int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_proposals WHERE skill_id=? AND version_id=? AND status IN ('rejected','discarded','withdrawn')`, skillID, ver.ID).Scan(&rejectedBefore)
	p := &Proposal{ID: id.New("prop"), SkillID: skillID, VersionID: ver.ID, Source: source, Category: category,
		Rationale: rationale, Risk: risk, Checks: checks, Status: "pending", CreatedAt: nowUTC()}
	if !Passed(checks) || rejectedBefore > 0 {
		p.Status = "rejected"
		if rejectedBefore > 0 {
			p.Checks = append(p.Checks, Check{Name: "not_previously_rejected", OK: false})
		}
	}
	cj, _ := json.Marshal(p.Checks)
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO skill_proposals(id, skill_id, version_id, source, category, rationale, risk, checks_json, status, created_at)
VALUES(?,?,?,?,?,?,?,?,?,?)`, p.ID, skillID, ver.ID, source, category, rationale, risk, string(cj), p.Status, p.CreatedAt); err != nil {
		return nil, err
	}
	if p.Status == "rejected" {
		return p, nil
	}
	st, _ := s.GetSettings(ctx, skillID)
	if st.Mode == "canary" && risk != "high" && source == "auto" {
		if err := s.StartCanary(ctx, p.ID); err != nil {
			return nil, err
		}
		p.Status = "canary"
		return p, nil
	}
	if source != "session" {
		s.emit(EventProposal, skillID, p.ID)
	}
	return p, nil
}

func (s *Service) proposal(ctx context.Context, pid string) (*Proposal, error) {
	var p Proposal
	var cj string
	err := s.DB.QueryRowContext(ctx, `SELECT id, skill_id, version_id, source, category, rationale, risk, checks_json, status, created_at FROM skill_proposals WHERE id=?`, pid).
		Scan(&p.ID, &p.SkillID, &p.VersionID, &p.Source, &p.Category, &p.Rationale, &p.Risk, &cj, &p.Status, &p.CreatedAt)
	if err != nil {
		return nil, errors.New("proposal not found")
	}
	_ = json.Unmarshal([]byte(cj), &p.Checks)
	return &p, nil
}

// Proposals lists proposals for a skill ("" = all).
func (s *Service) Proposals(ctx context.Context, skillID string) ([]Proposal, error) {
	q := `SELECT id FROM skill_proposals`
	var args []any
	if skillID != "" {
		q += ` WHERE skill_id=?`
		args = append(args, skillID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY created_at DESC LIMIT 100`, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var x string
		_ = rows.Scan(&x)
		ids = append(ids, x)
	}
	rows.Close()
	var out []Proposal
	for _, x := range ids {
		if p, err := s.proposal(ctx, x); err == nil {
			out = append(out, *p)
		}
	}
	return out, nil
}

// ErrStale: the skill's current version is no longer the one the proposal
// was made from (adopted elsewhere, edited, rolled back to another one), so
// promoting it would silently replace those changes.
var ErrStale = errors.New("the skill's current version changed since this version was made")

func setStatusTx(ctx context.Context, tx *sql.Tx, pid, st string) error {
	_, err := tx.ExecContext(ctx, `UPDATE skill_proposals SET status=?, decided_at=? WHERE id=?`, st, nowUTC(), pid)
	return err
}

// statusIn re-reads the proposal's status inside the move's transaction, so
// two clicks (two tabs) can't both promote / discard it.
func statusIn(ctx context.Context, tx *sql.Tx, pid string, ok ...string) error {
	var st string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM skill_proposals WHERE id=?`, pid).Scan(&st); err != nil {
		return errors.New("proposal not found")
	}
	for _, o := range ok {
		if st == o {
			return nil
		}
	}
	return errors.New("proposal is " + st)
}

// clearCanaryTx ends the skill's canary (when one is set) in tx.
func clearCanaryTx(ctx context.Context, tx *sql.Tx, skillID, actor, reason string) error {
	var c sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT version_id FROM pointers WHERE entity_type=? AND entity_id=? AND name=?`, version.EntitySkill, skillID, version.PointerCanary).Scan(&c)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (!c.Valid || c.String == "") {
		return nil
	}
	if err != nil {
		return err
	}
	return version.MovePointerTx(ctx, tx, version.EntitySkill, skillID, version.PointerCanary, "", actor, reason, "")
}

// StartCanary points `canary` at the proposal (only one canary per skill). The
// pointer and the proposal's status change together.
func (s *Service) StartCanary(ctx context.Context, pid string) error {
	p, err := s.proposal(ctx, pid)
	if err != nil {
		return err
	}
	if p.Status != "pending" && p.Status != "canary" {
		return errors.New("proposal is " + p.Status)
	}
	return s.Ver.MovePointerChecked(ctx, version.EntitySkill, p.SkillID, version.PointerCanary, p.VersionID, "user", "start canary", "",
		func(ctx context.Context, tx *sql.Tx, from string) error {
			if from != "" && from != p.VersionID {
				return errors.New("another canary is running for this skill")
			}
			return statusIn(ctx, tx, pid, "pending", "canary")
		},
		func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE skill_proposals SET status='canary' WHERE id=?`, pid)
			return err
		})
}

func (s *Service) activeCanaryProposal(ctx context.Context, skillID string) (*Proposal, error) {
	var pid string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM skill_proposals WHERE skill_id=? AND status='canary' ORDER BY created_at DESC LIMIT 1`, skillID).Scan(&pid)
	if err != nil {
		return nil, errors.New("no canary running")
	}
	return s.proposal(ctx, pid)
}

// Promote makes the proposal current (directly, or ending its canary).
func (s *Service) Promote(ctx context.Context, pid string) error {
	return s.PromoteOver(ctx, pid)
}

// PromoteOver is Promote only while the skill's current version is one of
// over (the version the proposal was made from, or the one the owner was shown
// and agreed to replace); otherwise ErrStale and nothing changes. No over: no
// check. Moving current, ending the canary and marking the proposal promoted
// are one transaction: before, a failed second or third write left a promoted
// version still "pending" or a canary still taking runs.
func (s *Service) PromoteOver(ctx context.Context, pid string, over ...string) error {
	p, err := s.proposal(ctx, pid)
	if err != nil {
		return err
	}
	if p.Status != "pending" && p.Status != "canary" {
		return errors.New("proposal is " + p.Status)
	}
	return s.Ver.MovePointerChecked(ctx, version.EntitySkill, p.SkillID, version.PointerCurrent, p.VersionID, "user", "promote proposal "+pid, "",
		func(ctx context.Context, tx *sql.Tx, from string) error {
			if err := statusIn(ctx, tx, pid, "pending", "canary"); err != nil {
				return err
			}
			if len(over) == 0 || from == p.VersionID {
				return nil
			}
			for _, o := range over {
				if o != "" && o == from {
					return nil
				}
			}
			return ErrStale
		},
		func(ctx context.Context, tx *sql.Tx) error {
			if err := clearCanaryTx(ctx, tx, p.SkillID, "user", "promoted"); err != nil {
				return err
			}
			return setStatusTx(ctx, tx, pid, "promoted")
		})
}

// Discard rejects a pending proposal or drops a running canary (the canary
// pointer and the status together: a discarded canary never keeps taking runs).
func (s *Service) Discard(ctx context.Context, pid string) error {
	p, err := s.proposal(ctx, pid)
	if err != nil {
		return err
	}
	switch p.Status {
	case "pending":
		return s.endProposal(ctx, p, false, "user", "reject proposal", "rejected", "pending")
	case "canary":
		return s.endProposal(ctx, p, true, "user", "discard canary", "discarded", "canary")
	default:
		return errors.New("proposal is " + p.Status)
	}
}

// endProposal sets the proposal's status (and, for a canary, ends the skill's
// canary) in one transaction, while it is still in one of from.
func (s *Service) endProposal(ctx context.Context, p *Proposal, canary bool, actor, reason, status string, from ...string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// write lock first: the status read below stays true until the commit
	if _, err := tx.ExecContext(ctx, `UPDATE skill_proposals SET status=status WHERE 0`); err != nil {
		return err
	}
	if err := statusIn(ctx, tx, p.ID, from...); err != nil {
		return err
	}
	if canary {
		if err := clearCanaryTx(ctx, tx, p.SkillID, actor, reason); err != nil {
			return err
		}
	}
	if err := setStatusTx(ctx, tx, p.ID, status); err != nil {
		return err
	}
	return tx.Commit()
}

// Extend adds n more canary runs.
func (s *Service) Extend(ctx context.Context, skillID string, n int) error {
	if n <= 0 {
		n = 10
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE skills SET canary_runs = canary_runs + ? WHERE id=?`, n, skillID)
	return err
}

// Choose picks the version a follow-binding uses for this run.
// noCanary is set for side-effect tasks, pinned bindings and dry runs.
func (s *Service) Choose(ctx context.Context, skillID string, noCanary bool) (versionID string, candidate bool, err error) {
	cur, err := s.Ver.CurrentVersion(ctx, version.EntitySkill, skillID)
	if err != nil {
		return "", false, err
	}
	if noCanary {
		return cur.ID, false, nil
	}
	c, _ := s.Ver.GetPointer(ctx, version.EntitySkill, skillID, version.PointerCanary)
	if !c.Valid || c.String == "" {
		return cur.ID, false, nil
	}
	st, _ := s.GetSettings(ctx, skillID)
	s.mu.Lock()
	r := s.rnd.Float64()
	s.mu.Unlock()
	if r < st.Ratio {
		return c.String, true, nil
	}
	return cur.ID, false, nil
}

// Outcome of one run of a skill version.
type Outcome struct {
	RunID      string
	SkillID    string
	VersionID  string
	Candidate  bool
	OK         bool
	Blocked    bool // danger block or budget breach
	CostUSD    float64
	DurationMS int64
}

// Record stores a run outcome and applies the withdrawal / completion rules.
func (s *Service) Record(ctx context.Context, o Outcome) error {
	if _, err := s.DB.ExecContext(ctx, `INSERT OR REPLACE INTO canary_runs(run_id, skill_id, version_id, is_candidate, ok, blocked, cost_usd, duration_ms, created_at)
VALUES(?,?,?,?,?,?,?,?,?)`, o.RunID, o.SkillID, o.VersionID, b2i(o.Candidate), b2i(o.OK), b2i(o.Blocked), o.CostUSD, o.DurationMS, nowUTC()); err != nil {
		return err
	}
	if !o.Candidate {
		return nil
	}
	p, err := s.activeCanaryProposal(ctx, o.SkillID)
	if err != nil || p.VersionID != o.VersionID {
		return nil
	}
	if why := s.shouldWithdraw(ctx, p, o); why != "" {
		if err := s.endProposal(ctx, p, true, "system", "auto-withdraw: "+why, "withdrawn", "canary"); err != nil {
			return err
		}
		s.emit(EventWithdrawn, p.SkillID, why)
		return nil
	}
	cmp, _ := s.Compare(ctx, p.SkillID)
	st, _ := s.GetSettings(ctx, p.SkillID)
	if cmp != nil && cmp.Candidate.Runs == st.Runs {
		s.emit(EventCanaryDone, p.SkillID, p.ID)
	}
	return nil
}

func (s *Service) shouldWithdraw(ctx context.Context, p *Proposal, o Outcome) string {
	if o.Blocked {
		return "dangerous operation blocked or budget exceeded"
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT ok FROM canary_runs WHERE skill_id=? AND version_id=? AND is_candidate=1 ORDER BY created_at DESC, rowid DESC LIMIT 2`, p.SkillID, p.VersionID)
	if err == nil {
		fails := 0
		n := 0
		for rows.Next() {
			var ok int
			_ = rows.Scan(&ok)
			n++
			if ok == 0 {
				fails++
			}
		}
		rows.Close()
		if n == 2 && fails == 2 {
			return "2 consecutive failures"
		}
	}
	cmp, err := s.Compare(ctx, p.SkillID)
	if err == nil && cmp.Candidate.Runs >= 3 && cmp.Current.Runs > 0 {
		if (1-cmp.Candidate.SuccessRate)-(1-cmp.Current.SuccessRate) >= 0.20 {
			return "failure rate 20+ points above current"
		}
	}
	return ""
}

// Stats summarizes runs of one side.
type Stats struct {
	VersionID   string  `json:"version_id"`
	Runs        int     `json:"runs"`
	SuccessRate float64 `json:"success_rate"`
	P50MS       int64   `json:"p50_ms"`
	P95MS       int64   `json:"p95_ms"`
	AvgCostUSD  float64 `json:"avg_cost_usd"`
	Blocked     int     `json:"blocked"`
}

// Comparison is the canary panel data.
type Comparison struct {
	ProposalID string `json:"proposal_id"`
	Current    Stats  `json:"current"`
	Candidate  Stats  `json:"candidate"`
	Note       string `json:"note,omitempty"`
}

// Compare returns current vs candidate since the canary started.
func (s *Service) Compare(ctx context.Context, skillID string) (*Comparison, error) {
	p, err := s.activeCanaryProposal(ctx, skillID)
	if err != nil {
		return nil, err
	}
	since := p.CreatedAt
	cur, _ := s.Ver.CurrentVersion(ctx, version.EntitySkill, skillID)
	side := func(vid string, cand int) Stats {
		st := Stats{VersionID: vid}
		rows, err := s.DB.QueryContext(ctx, `SELECT ok, blocked, cost_usd, duration_ms FROM canary_runs WHERE skill_id=? AND is_candidate=? AND created_at >= ?`, skillID, cand, since)
		if err != nil {
			return st
		}
		defer rows.Close()
		var durs []int64
		okN := 0
		var cost float64
		for rows.Next() {
			var ok, bl int
			var c float64
			var d int64
			_ = rows.Scan(&ok, &bl, &c, &d)
			st.Runs++
			okN += ok
			st.Blocked += bl
			cost += c
			durs = append(durs, d)
		}
		if st.Runs > 0 {
			st.SuccessRate = float64(okN) / float64(st.Runs)
			st.AvgCostUSD = cost / float64(st.Runs)
			sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
			st.P50MS = durs[(len(durs)-1)/2]
			st.P95MS = durs[(len(durs)-1)*95/100]
		}
		return st
	}
	c := &Comparison{ProposalID: p.ID, Candidate: side(p.VersionID, 1)}
	if cur != nil {
		c.Current = side(cur.ID, 0)
	}
	if c.Candidate.Runs < 10 {
		c.Note = "low_sample"
	}
	return c, nil
}

// ShouldAnalyze applies the long-run trigger: mode propose/canary and ≥20 production runs over
// ≥7 days since the last analysis, or a ≥30% deviation from the baseline failure rate.
func (s *Service) ShouldAnalyze(ctx context.Context, skillID string, now time.Time) (bool, string) {
	var mode string
	var last sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT optimize_mode, last_analyzed_at FROM skills WHERE id=?`, skillID).Scan(&mode, &last); err != nil || mode == "off" {
		return false, ""
	}
	since := "0000"
	if last.Valid {
		since = last.String
	}
	var n int
	var first sql.NullString
	var okRate sql.NullFloat64
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*), MIN(created_at), AVG(ok) FROM canary_runs WHERE skill_id=? AND is_candidate=0 AND created_at > ?`, skillID, since).Scan(&n, &first, &okRate)
	if n >= MinRunsForAnalysis && first.Valid {
		if t, err := time.Parse(time.RFC3339, first.String); err == nil && now.Sub(t) >= MinDaysForAnalysis*24*time.Hour {
			return true, "long_run"
		}
	}
	var base sql.NullFloat64
	_ = s.DB.QueryRowContext(ctx, `SELECT AVG(ok) FROM canary_runs WHERE skill_id=? AND is_candidate=0 AND created_at <= ?`, skillID, since).Scan(&base)
	if n >= 5 && base.Valid && okRate.Valid && (1-okRate.Float64)-(1-base.Float64) >= DeviationThreshold {
		return true, "deviation"
	}
	return false, ""
}

// MarkAnalyzed records the analysis time.
func (s *Service) MarkAnalyzed(ctx context.Context, skillID string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE skills SET last_analyzed_at=? WHERE id=?`, nowUTC(), skillID)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nowUTC is a fixed-width UTC timestamp: the queries compare created_at as text, and
// RFC3339Nano drops trailing zeros ("…:14.1Z" sorts after "…:14.12Z"), which made a canary's
// own runs look older than the canary now and then.
func nowUTC() string { return time.Now().UTC().Format(tsLayout) }

const tsLayout = "2006-01-02T15:04:05.000000000Z"
