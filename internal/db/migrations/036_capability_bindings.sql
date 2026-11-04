CREATE TABLE capability_bindings (
 owner_kind TEXT NOT NULL CHECK(owner_kind IN ('task','workflow_node','button','session')),
 owner_id TEXT NOT NULL,
 capability_id TEXT NOT NULL REFERENCES capabilities(id),
 version_id TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 PRIMARY KEY(owner_kind,owner_id,capability_id)
);
CREATE INDEX capability_binding_cap ON capability_bindings(capability_id);
CREATE TABLE capability_run_bindings (
 run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
 capability_id TEXT NOT NULL REFERENCES capabilities(id),
 version_id TEXT NOT NULL REFERENCES capability_versions(id),
 PRIMARY KEY(run_id,capability_id)
);
