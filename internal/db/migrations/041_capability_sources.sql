-- Discovery metadata only. No trust grants, credentials or install authority.
CREATE TABLE capability_sources (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('marketplace','mcp_registry')),
 source TEXT NOT NULL,
 cached_json TEXT NOT NULL DEFAULT '[]',
 refreshed_at TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 UNIQUE(kind,source)
);
