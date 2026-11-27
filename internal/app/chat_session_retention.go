package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// emptySessionPruneMax is how many unused empty chats one cleanup pass may
// remove. A large backlog is worked off over later 6-hour ticks.
const emptySessionPruneMax = 200

// emptyChatSessions lists unused blank chats that the current log-day policy
// would delete. LogDays 0 keeps them forever, same as idle transcripts and
// scratch folders. A session is only selected when every signal says the user
// did not start or keep it:
//
//   - origin is "chat" (not continued from a run)
//   - no turns (counter and chat_session_turns both empty)
//   - no title (a name is treated as the user keeping it)
//   - no workspace folder of their own
//   - no skill or capability bindings (including session defaults)
//   - no capability proposals
//   - no skill draft on disk
//   - never started a Claude session and no recorded cost
//   - idle, and both created_at and updated_at older than LogDays
//
// Opening the list does not touch updated_at; changing the permission level,
// bindings, folder or title does, and those sessions stay.
func (a *App) emptyChatSessions(ctx context.Context, p RetentionPolicy, now time.Time) ([]string, error) {
	if p.LogDays <= 0 {
		return nil, nil
	}
	rows, err := a.DB.QueryContext(ctx, `
SELECT id, bindings_json, created_at, updated_at
FROM chat_sessions
WHERE origin='chat'
  AND status='idle'
  AND turns=0
  AND cost_total<=0
  AND TRIM(title)=''
  AND workspace_dir=''
  AND claude_session_id=''
  AND NOT EXISTS (SELECT 1 FROM chat_session_turns t WHERE t.session_id=chat_sessions.id)
  AND NOT EXISTS (SELECT 1 FROM capability_proposals p WHERE p.session_id=chat_sessions.id)
  AND NOT EXISTS (SELECT 1 FROM capability_bindings b WHERE b.owner_kind='session' AND b.owner_id=chat_sessions.id)
ORDER BY updated_at, id
LIMIT ?`, emptySessionPruneMax)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cut := time.Duration(p.LogDays) * 24 * time.Hour
	var ids []string
	for rows.Next() {
		var id, raw, created, updated string
		if err = rows.Scan(&id, &raw, &created, &updated); err != nil {
			return nil, err
		}
		if !a.emptyChatBindings(raw) {
			continue
		}
		if now.Sub(parseRetentionTime(created, now)) <= cut || now.Sub(parseRetentionTime(updated, now)) <= cut {
			continue
		}
		if a.chatDraftPresent(id) {
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (a *App) emptyChatBindings(raw string) bool {
	b := parseChatBindings(raw)
	return len(b.Skills) == 0 && len(b.Capabilities) == 0
}

// chatDraftPresent is true when the session has draft.json or a skill draft
// folder (drafts/, or an older session's draft/). An empty leftover folder
// still counts: the user may have just started "改技能".
func (a *App) chatDraftPresent(sid string) bool {
	if _, err := os.Lstat(a.chatDraftStatePath(sid)); err == nil {
		return true
	}
	for _, dir := range []string{a.chatDraftsDir(sid), a.chatLegacyDraftDir(sid)} {
		if fi, err := os.Lstat(dir); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// pruneEmptyChatSessions deletes unused blank chats under the policy (see
// emptyChatSessions). Each delete re-checks the same conditions so a turn,
// title, binding or draft started after the list was built is left alone.
// Session folders and transcripts go with the row, as in DeleteChatSession.
func (a *App) pruneEmptyChatSessions(ctx context.Context, p RetentionPolicy, now time.Time) (int64, error) {
	ids, err := a.emptyChatSessions(ctx, p, now)
	if err != nil {
		return 0, err
	}
	var n int64
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		ok, err := a.deleteEmptyChatSession(ctx, id)
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}

func (a *App) deleteEmptyChatSession(ctx context.Context, sid string) (bool, error) {
	chatDraftMu.Lock()
	defer chatDraftMu.Unlock()
	if a.chatDraftPresent(sid) {
		return false, nil
	}
	var raw, created, updated string
	err := a.DB.QueryRowContext(ctx, `SELECT bindings_json, created_at, updated_at FROM chat_sessions
WHERE id=? AND origin='chat' AND status='idle' AND turns=0 AND cost_total<=0
  AND TRIM(title)='' AND workspace_dir='' AND claude_session_id=''
  AND NOT EXISTS (SELECT 1 FROM chat_session_turns t WHERE t.session_id=chat_sessions.id)
  AND NOT EXISTS (SELECT 1 FROM capability_proposals p WHERE p.session_id=chat_sessions.id)
  AND NOT EXISTS (SELECT 1 FROM capability_bindings b WHERE b.owner_kind='session' AND b.owner_id=chat_sessions.id)`, sid).
		Scan(&raw, &created, &updated)
	if err != nil || !a.emptyChatBindings(raw) {
		return false, nil
	}
	res, err := a.DB.ExecContext(ctx, `DELETE FROM chat_sessions
WHERE id=? AND origin='chat' AND status='idle' AND turns=0 AND cost_total<=0
  AND TRIM(title)='' AND workspace_dir='' AND claude_session_id=''
  AND updated_at=? AND created_at=?
  AND NOT EXISTS (SELECT 1 FROM chat_session_turns t WHERE t.session_id=chat_sessions.id)
  AND NOT EXISTS (SELECT 1 FROM capability_proposals p WHERE p.session_id=chat_sessions.id)
  AND NOT EXISTS (SELECT 1 FROM capability_bindings b WHERE b.owner_kind='session' AND b.owner_id=chat_sessions.id)`,
		sid, updated, created)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return false, nil
	}
	_, _ = a.DB.ExecContext(ctx, `DELETE FROM capability_proposals WHERE session_id=? AND status<>'applying'`, sid)
	a.removeChatSessionDisk(sid, "")
	return true, nil
}

// removeChatSessionDisk drops the session folder and any tidebot-owned
// transcripts. A folder still held open is left for planChatSessionDirRetention.
func (a *App) removeChatSessionDisk(sid, claudeSessionID string) {
	for _, p := range a.chatTranscripts(claudeSessionID) {
		_ = os.Remove(p)
		_ = os.RemoveAll(strings.TrimSuffix(p, ".jsonl"))
	}
	if err := os.RemoveAll(filepath.Join(a.chatSessionsDir(), safeChatID(sid))); err != nil && a.Log != nil {
		a.Log.Warn("delete session folder, retried by cleanup", "session", sid, "error", err)
	}
}
