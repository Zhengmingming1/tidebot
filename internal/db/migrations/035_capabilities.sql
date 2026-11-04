CREATE TABLE capabilities (
 id TEXT PRIMARY KEY,
 title TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'installed' CHECK(status IN ('installed','uninstalled')),
 current_version_id TEXT NOT NULL,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE TABLE capability_versions (
 id TEXT PRIMARY KEY,
 capability_id TEXT NOT NULL REFERENCES capabilities(id),
 lock_json TEXT NOT NULL,
 lock_sha256 TEXT NOT NULL,
 files_json TEXT NOT NULL,
 created_at TEXT NOT NULL
);
CREATE INDEX capability_versions_cap ON capability_versions(capability_id,created_at);
CREATE TABLE capability_events (
 id TEXT PRIMARY KEY,
 capability_id TEXT NOT NULL,
 version_id TEXT NOT NULL DEFAULT '',
 actor TEXT NOT NULL,
 action TEXT NOT NULL,
 verdict TEXT NOT NULL DEFAULT '',
 result TEXT NOT NULL,
 created_at TEXT NOT NULL
);
CREATE INDEX capability_events_cap ON capability_events(capability_id,created_at);
CREATE TABLE capability_proposals (
 id TEXT PRIMARY KEY,
 session_id TEXT NOT NULL,
 run_id TEXT NOT NULL DEFAULT '',
 base_version_id TEXT NOT NULL DEFAULT '',
 kind TEXT NOT NULL CHECK(kind IN ('install','update','uninstall','bind','fix','skill_optimize')),
 payload_json TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','applying','applied','dismissed','expired','failed')),
 decided_by TEXT NOT NULL DEFAULT '',
 decided_at TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 expires_at TEXT NOT NULL
);
CREATE INDEX capability_proposals_session ON capability_proposals(session_id,created_at);

CREATE TRIGGER capability_versions_no_update BEFORE UPDATE ON capability_versions
BEGIN SELECT RAISE(ABORT, 'capability versions are immutable'); END;
CREATE TRIGGER capability_events_no_update BEFORE UPDATE ON capability_events
BEGIN SELECT RAISE(ABORT, 'capability events are append-only'); END;
CREATE TRIGGER capability_events_no_delete BEFORE DELETE ON capability_events
BEGIN SELECT RAISE(ABORT, 'capability events are append-only'); END;
