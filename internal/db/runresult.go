package db

import (
	"context"
	"strings"
	"unicode/utf8"
)

// MaxRunResultBytes caps the final answer kept per run. Agents usually answer in a few KB;
// the cap keeps a runaway answer from growing the database.
const MaxRunResultBytes = 128 << 10

// CapRunResult trims text to MaxRunResultBytes on a UTF-8 boundary and normalises line
// endings to \n (an answer written on Windows reads the same everywhere).
func CapRunResult(text string) (string, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if len(text) <= MaxRunResultBytes {
		return text, false
	}
	cut := MaxRunResultBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true
}

// SetRunResult stores the run's final answer (capped).
func (s *Store) SetRunResult(ctx context.Context, runID, text string) error {
	text, cut := CapRunResult(text)
	_, err := s.DB.ExecContext(ctx, `UPDATE runs SET result_text=?, result_truncated=? WHERE id=?`, text, boolToInt(cut), runID)
	return err
}

// RunResult returns the stored final answer and whether it was cut.
func (s *Store) RunResult(ctx context.Context, runID string) (string, bool, error) {
	var text string
	var cut int
	err := s.DB.QueryRowContext(ctx, `SELECT result_text, result_truncated FROM runs WHERE id=?`, runID).Scan(&text, &cut)
	return text, cut != 0, err
}
