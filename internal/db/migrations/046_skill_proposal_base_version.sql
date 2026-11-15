-- Record the skill's current version when a proposal is made, so promoting
-- it can refuse if current has moved on (same check as session adopt).
-- Existing rows get the parent of the proposed version, which Commit set
-- to current at that time.
ALTER TABLE skill_proposals ADD COLUMN base_version_id TEXT NOT NULL DEFAULT '';

UPDATE skill_proposals
SET base_version_id = COALESCE((
  SELECT v.parent_version_id FROM versions v WHERE v.id = skill_proposals.version_id
), '')
WHERE base_version_id = '';
