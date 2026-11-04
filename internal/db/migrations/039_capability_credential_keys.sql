-- Names only: credential values remain in the encrypted secret backend. This
-- inventory survives snapshot GC, so removed components can still be revoked.
CREATE TABLE capability_credential_keys (
 capability_id TEXT NOT NULL REFERENCES capabilities(id),
 kind TEXT NOT NULL CHECK(kind IN ('secret','oauth')),
 name TEXT NOT NULL CHECK(name<>''),
 PRIMARY KEY(capability_id,kind,name)
);
INSERT OR IGNORE INTO capability_credential_keys(capability_id,kind,name)
SELECT v.capability_id,'secret',json_extract(s.value,'$.name')
FROM capability_versions v,json_each(v.lock_json,'$.requires.secrets') s
WHERE coalesce(json_extract(s.value,'$.name'),'')<>'';
INSERT OR IGNORE INTO capability_credential_keys(capability_id,kind,name)
SELECT v.capability_id,'oauth',json_extract(c.value,'$.name')
FROM capability_versions v,json_each(v.lock_json,'$.components') c
WHERE json_extract(c.value,'$.type')='mcp'
AND json_extract(c.value,'$.mcp.type') IN ('http','sse','streamable-http')
AND coalesce(json_extract(c.value,'$.name'),'')<>'';
CREATE TRIGGER capability_credential_keys_remember AFTER INSERT ON capability_versions
BEGIN
 INSERT OR IGNORE INTO capability_credential_keys(capability_id,kind,name)
 SELECT NEW.capability_id,'secret',json_extract(s.value,'$.name')
 FROM json_each(NEW.lock_json,'$.requires.secrets') s
 WHERE coalesce(json_extract(s.value,'$.name'),'')<>'';
 INSERT OR IGNORE INTO capability_credential_keys(capability_id,kind,name)
 SELECT NEW.capability_id,'oauth',json_extract(c.value,'$.name')
 FROM json_each(NEW.lock_json,'$.components') c
 WHERE json_extract(c.value,'$.type')='mcp'
 AND json_extract(c.value,'$.mcp.type') IN ('http','sse','streamable-http')
 AND coalesce(json_extract(c.value,'$.name'),'')<>'';
END;
