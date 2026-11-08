package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/id"
)

var ErrProposalConflict = errors.New("proposal is no longer pending or belongs to another session")

type SkillOptimization struct {
	SkillID     string `json:"skill_id"`
	VersionID   string `json:"version_id"`
	Instruction string `json:"instruction"`
}

type proposalPayload struct {
	SkillReconciliation []SkillReconciliation `json:"skill_reconciliation,omitempty"`
	Diff                *Diff                 `json:"diff,omitempty"`
	UninstallPreview    *UninstallResult      `json:"uninstall_preview,omitempty"`
	Lock
	SkillOptimization *SkillOptimization `json:"skill_optimization,omitempty"`
}

type Proposal struct {
	SkillReconciliation []SkillReconciliation `json:"skill_reconciliation,omitempty"`
	Diff                *Diff                 `json:"diff,omitempty"`
	UninstallPreview    *UninstallResult      `json:"uninstall_preview,omitempty"`
	SkillOptimization   *SkillOptimization    `json:"skill_optimization,omitempty"`
	BaseVersionID       string                `json:"base_version_id,omitempty"`
	ID                  string                `json:"id"`
	SessionID           string                `json:"session_id"`
	RunID               string                `json:"run_id"`
	Kind                string                `json:"kind"`
	Status              string                `json:"status"`
	DecidedBy           string                `json:"decided_by"`
	DecidedAt           string                `json:"decided_at"`
	CreatedAt           string                `json:"created_at"`
	ExpiresAt           string                `json:"expires_at"`
	Lock                Lock                  `json:"lock"`
}

func (s *Store) CreateProposal(ctx context.Context, p Proposal) (Proposal, error) {
	if err := ValidatePermissions(p.Lock); err != nil {
		return p, err
	}
	if p.SessionID == "" {
		return p, errors.New("proposal requires a session")
	}
	switch p.Kind {
	case "install", "update", "uninstall", "bind", "fix", "skill_optimize":
	default:
		return p, errors.New("invalid proposal kind")
	}
	if p.Kind == "update" {
		current, e := s.Get(ctx, p.Lock.ID)
		if e != nil {
			return p, e
		}
		if p.BaseVersionID != "" && p.BaseVersionID != current.VersionID {
			return p, errors.New("capability changed; inspect update again")
		}
		p.BaseVersionID = current.VersionID
		p.SkillReconciliation, e = reconcileStates(current.LocalSkills, p.Lock)
		if e != nil {
			return p, e
		}
	}
	raw, err := json.Marshal(proposalPayload{SkillReconciliation: p.SkillReconciliation, Lock: p.Lock, SkillOptimization: p.SkillOptimization, UninstallPreview: p.UninstallPreview, Diff: p.Diff})
	if err != nil {
		return p, err
	}
	if len(raw) > 1<<20 {
		return p, errors.New("proposal too large")
	}
	now := time.Now().UTC()
	p.ID = id.New("capprop")
	p.Status = "pending"
	p.CreatedAt = now.Format(time.RFC3339Nano)
	p.ExpiresAt = now.Add(24 * time.Hour).Format(time.RFC3339Nano)
	p.DecidedBy = ""
	p.DecidedAt = ""
	_, err = s.DB.ExecContext(ctx, `INSERT INTO capability_proposals(id,session_id,run_id,kind,payload_json,status,created_at,expires_at,base_version_id) VALUES(?,?,?,?,?,'pending',?,?,?)`, p.ID, p.SessionID, p.RunID, p.Kind, string(raw), p.CreatedAt, p.ExpiresAt, p.BaseVersionID)
	return p, err
}

type rowScanner interface{ Scan(...any) error }

func readProposal(row rowScanner) (Proposal, error) {
	var p Proposal
	var raw string
	err := row.Scan(&p.ID, &p.SessionID, &p.RunID, &p.Kind, &raw, &p.Status, &p.DecidedBy, &p.DecidedAt, &p.CreatedAt, &p.ExpiresAt, &p.BaseVersionID)
	if err == nil {
		var payload proposalPayload
		err = json.Unmarshal([]byte(raw), &payload)
		p.Lock, p.SkillOptimization = payload.Lock, payload.SkillOptimization
		p.UninstallPreview = payload.UninstallPreview
		p.Diff = payload.Diff
		p.SkillReconciliation = payload.SkillReconciliation
	}
	return p, err
}

const proposalColumns = `id,session_id,run_id,kind,payload_json,status,decided_by,decided_at,created_at,expires_at,base_version_id`

// ProposalSigQuery lists what can change about a session's cards (a new card,
// or a status change, which always stamps decided_at). Payloads never change
// after insert. The covering index answers it without touching payload rows.
const ProposalSigQuery = `SELECT id,status,decided_at FROM capability_proposals WHERE session_id=? ORDER BY created_at DESC LIMIT 200`

func (s *Store) GetProposal(ctx context.Context, proposalID string) (Proposal, error) {
	return readProposal(s.DB.QueryRowContext(ctx, `SELECT `+proposalColumns+` FROM capability_proposals WHERE id=?`, proposalID))
}

// ProposalsSig is a short fingerprint of a session's card list; equal sigs
// mean ListProposals would return the same cards.
func (s *Store) ProposalsSig(ctx context.Context, sessionID string) (string, error) {
	rows, err := s.DB.QueryContext(ctx, ProposalSigQuery, sessionID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	h := sha256.New()
	h.Write([]byte(sessionID))
	for rows.Next() {
		var id, status, decided string
		if err := rows.Scan(&id, &status, &decided); err != nil {
			return "", err
		}
		h.Write([]byte{0})
		h.Write([]byte(id + "\x1f" + status + "\x1f" + decided))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)[:12]), nil
}

func (s *Store) ListProposals(ctx context.Context, sessionID string) ([]Proposal, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+proposalColumns+` FROM capability_proposals WHERE session_id=? ORDER BY created_at DESC LIMIT 200`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Proposal{}
	for rows.Next() {
		p, e := readProposal(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClaimProposal atomically claims exactly one pending proposal, scoped to the
// authenticated UI's session. Models must never be given this operation.
func (s *Store) ClaimProposal(ctx context.Context, proposalID, sessionID, actor string) (Proposal, error) {
	if actor == "" {
		return Proposal{}, errors.New("decision requires an actor")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.DB.ExecContext(ctx, `UPDATE capability_proposals SET status='applying',decided_by=?,decided_at=? WHERE id=? AND session_id=? AND status='pending' AND expires_at>?`, actor, now, proposalID, sessionID, now)
	if err != nil {
		return Proposal{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return Proposal{}, err
	}
	if n != 1 {
		return Proposal{}, ErrProposalConflict
	}
	return s.GetProposal(ctx, proposalID)
}
func (s *Store) FinishProposal(ctx context.Context, proposalID, actor, status string) error {
	switch status {
	case "applied", "dismissed", "failed":
	default:
		return errors.New("invalid final proposal status")
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE capability_proposals SET status=?,decided_at=? WHERE id=? AND status='applying' AND decided_by=?`, status, time.Now().UTC().Format(time.RFC3339Nano), proposalID, actor)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrProposalConflict
	}
	return nil
}
