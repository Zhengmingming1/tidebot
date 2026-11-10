package capability

import "errors"

// TrustUser is a source the user picked themselves: an uploaded archive or folder, a local
// directory, or a Git repository they added. Same scan as extension installs; low and
// caution results import directly, high risk needs the user's explicit confirmation.
const TrustUser = "user"

// ErrRiskConfirm means the scan found high-risk content and the import was not confirmed.
var ErrRiskConfirm = errors.New("high-risk content: confirm to import")

// maxImportFindings bounds what an import preview returns (the verdict covers all findings).
const maxImportFindings = 20

// ImportReview is the scan and policy a skill import shows the user.
type ImportReview struct {
	Scan   Scan   `json:"scan"`
	Policy Policy `json:"policy"`
}

// ReviewImport scans files with the extension scanner and applies the install policy
// for a user-picked source. The returned findings are capped for display.
func ReviewImport(files map[string][]byte) ImportReview {
	sc := ScanFiles(files)
	pol := InstallPolicy(TrustUser, sc.Verdict, false)
	if len(sc.Findings) > maxImportFindings {
		sc.Findings = sc.Findings[:maxImportFindings]
	}
	return ImportReview{Scan: sc, Policy: pol}
}

// CheckImport rescans files at import time (never trusting an earlier preview) and refuses
// unconfirmed high-risk content.
func CheckImport(files map[string][]byte, confirmed bool) (ImportReview, error) {
	rv := ReviewImport(files)
	if !rv.Policy.Allowed {
		return rv, errors.New(rv.Policy.Reason)
	}
	if rv.Policy.SecondConfirmationRequired && !confirmed {
		return rv, ErrRiskConfirm
	}
	return rv, nil
}
