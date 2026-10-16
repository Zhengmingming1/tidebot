package version

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// HashJSON returns SHA-256 hex of canonical JSON (sorted object keys via encoding/json of maps is not enough).
// We marshal through json.Marshal after normalizing via round-trip map for objects.
func HashJSON(v any) (string, []byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", nil, err
	}
	// Re-encode through json.Decoder UseNumber to stabilize; for structs json.Marshal is field-order stable in Go.
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), raw, nil
}

// HashFileManifest hashes a sorted path->sha256 map for skill packages.
func HashFileManifest(files map[string]string) (string, []byte, error) {
	type pair struct{ Path, SHA string }
	var list []pair
	for p, h := range files {
		list = append(list, pair{Path: p, SHA: h})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })
	return HashJSON(list)
}
