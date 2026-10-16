// Package id generates prefixed ULID-style identifiers used as primary keys.
package id

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// New returns a prefixed identifier: "<prefix>_<hex timestamp><random>".
// Format is compact, sortable by time, and unique enough for local SQLite use.
func New(prefix string) string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	ts := time.Now().UTC().UnixMilli()
	return fmt.Sprintf("%s_%x%s", prefix, ts, hex.EncodeToString(b[:]))
}
