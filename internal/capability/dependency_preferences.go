package capability

// DependencyPreference is a machine-local owner choice captured with a proposal.
// It selects an executable, not permission to download or install anything.
type DependencyPreference struct {
	Path          string `json:"path,omitempty"`
	PreferManaged bool   `json:"prefer_managed"`
}
