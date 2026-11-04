-- Owner-only dependency choices. These affect new reviews, never existing locks.
CREATE TABLE capability_dependency_preferences (
 name TEXT PRIMARY KEY,
 path TEXT NOT NULL DEFAULT '',
 prefer_managed INTEGER NOT NULL DEFAULT 0 CHECK(prefer_managed IN (0,1)),
 updated_at TEXT NOT NULL
);
