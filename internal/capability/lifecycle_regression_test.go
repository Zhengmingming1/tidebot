package capability

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestReinstallProposalPreservesHistory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	first, err := s.Install(ctx, testLock(), testFiles())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Uninstall(ctx, first.ID, "ui"); err != nil {
		t.Fatal(err)
	}
	createSession(t, s, `{}`)
	l := testLock()
	_, l.FilesSHA256, _ = HashFiles(testFiles())
	p, err := s.CreateProposal(ctx, Proposal{SessionID: "session", Kind: "install", Lock: l})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimProposal(ctx, p.ID, "session", "reinstaller"); err != nil {
		t.Fatal(err)
	}
	second, err := s.InstallProposal(ctx, p.ID, "session", "reinstaller", l, testFiles())
	if err != nil {
		t.Fatal(err)
	}
	if first.VersionID == second.VersionID || first.CreatedAt != second.CreatedAt {
		t.Fatal("reinstall replaced original identity/history")
	}
	versions, err := s.Versions(ctx, first.ID)
	if err != nil || len(versions) != 2 {
		t.Fatal(versions, err)
	}
	if err = s.Verify(ctx, first.ID, first.VersionID); err != nil {
		t.Fatal(err)
	}
	current, err := s.Get(ctx, first.ID)
	if err != nil || current.VersionID != second.VersionID {
		t.Fatal(current, err)
	}
	events, err := s.Events(ctx, first.ID)
	if err != nil || len(events) != 3 {
		t.Fatal(events, err)
	}
	found := false
	for _, event := range events {
		if event.Action == "install" && event.VersionID == second.VersionID && event.Actor == "reinstaller" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing authenticated reinstall audit", events)
	}
	p, err = s.GetProposal(ctx, p.ID)
	if err != nil || p.Status != "applied" {
		t.Fatal(p, err)
	}
}

func TestUpdateAndInstallRecomputeAdapterScan(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.Install(ctx, testLock(), testFiles()); err != nil {
		t.Fatal(err)
	}
	createSession(t, s, `{}`)
	calls := 0
	s.ScanSource = func(source Source, files map[string][]byte) (Scan, error) {
		calls++
		if source.Adapter != "claude-plugin" {
			return Scan{}, errors.New("wrong adapter")
		}
		scan := ScanFiles(files)
		scan.Findings = append(scan.Findings, Finding{ID: "unsupported-hooks", Severity: "caution", Note: "Hooks disabled"})
		scan.Verdict = "caution"
		return scan, nil
	}
	l := testLock()
	l.Source.Adapter = "claude-plugin"
	l.Scan = Scan{Verdict: "safe", Findings: []Finding{{ID: "untrusted-manifest-finding"}}}
	p, d, err := s.CreateUpdateProposal(ctx, "session", "", l, testFiles())
	if err != nil {
		t.Fatal(err)
	}
	if p.Lock.Scan.Verdict != "caution" || len(p.Lock.Scan.Findings) != 1 || p.Lock.Scan.Findings[0].ID != "unsupported-hooks" {
		t.Fatal(p.Lock.Scan)
	}
	a, _ := json.Marshal(d.Scan)
	b, _ := json.Marshal(p.Lock.Scan)
	if string(a) != string(b) {
		t.Fatal("diff disagrees with proposal")
	}
	if _, err = s.ClaimProposal(ctx, p.ID, "session", "ui"); err != nil {
		t.Fatal(err)
	}
	rec, err := s.InstallProposal(ctx, p.ID, "session", "ui", p.Lock, testFiles())
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(rec.Lock.Scan)
	if string(a) != string(b) || calls != 2 {
		t.Fatal("stored scan lost adapter findings", rec.Lock.Scan, calls)
	}
}

func TestAdapterScanFailsClosedWithoutScanner(t *testing.T) {
	s := testStore(t)
	l := testLock()
	l.Source.Adapter = "claude-plugin"
	if _, err := s.Install(context.Background(), l, testFiles()); err == nil {
		t.Fatal("accepted unscanned adapter")
	}
}
