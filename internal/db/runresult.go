package db

import (
	"context"
	"database/sql"
	"errors"
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

// AnswerKey is what "the same answer" compares: the text with every run of whitespace
// (spaces, tabs, blank lines, a trailing newline) folded into one space, so re-wrapped or
// re-indented output is not a change. Idea: changedetection.io
// processors/text_json_diff/processor.py (ChecksumCalculator with ignore_whitespace).
func AnswerKey(text string) string { return strings.Join(strings.Fields(text), " ") }

// MarkAnswerSame compares a finished run's final answer with the task's previous succeeded
// run that has one, stores the earlier run's id on this run when they match and returns it
// ("" when the answer changed, is the task's first, is empty, or the run has no task).
func (s *Store) MarkAnswerSame(ctx context.Context, runID string) (string, error) {
	var taskID sql.NullString
	var created, text string
	if err := s.DB.QueryRowContext(ctx, `SELECT task_id, created_at, result_text FROM runs WHERE id=?`, runID).Scan(&taskID, &created, &text); err != nil {
		return "", err
	}
	key := AnswerKey(text)
	if !taskID.Valid || taskID.String == "" || key == "" {
		return "", nil
	}
	prevID, prevText, _, err := s.prevAnswer(ctx, taskID.String, runID, created)
	if err != nil || prevID == "" {
		return "", err
	}
	same := ""
	if AnswerKey(prevText) == key {
		same = prevID
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE runs SET answer_same_as=? WHERE id=?`, same, runID)
	return same, err
}

// PrevAnswer returns the run the given run's answer is compared with: the task's previous
// succeeded run that has an answer (its id, answer and finish time). Empty id when there is
// none (first answer, a quick run without a task, or the run does not exist).
func (s *Store) PrevAnswer(ctx context.Context, runID string) (id, text, at string, err error) {
	var taskID sql.NullString
	var created string
	if err = s.DB.QueryRowContext(ctx, `SELECT task_id, created_at FROM runs WHERE id=?`, runID).Scan(&taskID, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		return
	}
	if !taskID.Valid || taskID.String == "" {
		return
	}
	return s.prevAnswer(ctx, taskID.String, runID, created)
}

func (s *Store) prevAnswer(ctx context.Context, taskID, runID, created string) (id, text, at string, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT id, result_text, COALESCE(finished_at, created_at) FROM runs
WHERE task_id=? AND id<>? AND status='succeeded' AND result_text<>'' AND created_at<=?
ORDER BY created_at DESC, id DESC LIMIT 1`, taskID, runID, created).Scan(&id, &text, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", nil
	}
	return
}
