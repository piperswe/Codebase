CREATE SCHEMA memory;
CREATE SCHEMA memory_auth;
CREATE TABLE memory.spaces (
 id uuid PRIMARY KEY, slug text UNIQUE NOT NULL CHECK (slug ~ '^[a-z][a-z0-9-]{0,62}$'),
 revision bigint NOT NULL DEFAULT 0 CHECK(revision>=0), schema_revision bigint NOT NULL DEFAULT 0 CHECK(schema_revision>=0)
);
CREATE TABLE memory.entities (
 space_id uuid NOT NULL REFERENCES memory.spaces(id), id uuid NOT NULL,
 kind text NOT NULL CHECK(kind IN ('ordinary','attribute','type','source')),
 created_revision bigint NOT NULL, revision bigint NOT NULL, archived_revision bigint,
 PRIMARY KEY(space_id,id), UNIQUE(space_id,id,kind)
);
CREATE INDEX entity_kind ON memory.entities(space_id,kind,id);
CREATE TABLE memory.entity_names (
 space_id uuid NOT NULL, entity_id uuid NOT NULL, name text NOT NULL CHECK(length(name)<=200 AND name ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*/[a-z][a-z0-9]*(-[a-z0-9]+)*$'),
 is_canonical boolean NOT NULL DEFAULT false,
 PRIMARY KEY(space_id,name), FOREIGN KEY(space_id,entity_id) REFERENCES memory.entities(space_id,id)
);
CREATE UNIQUE INDEX entity_one_canonical_name ON memory.entity_names(space_id,entity_id) WHERE is_canonical;
CREATE TABLE memory.type_defs (
 space_id uuid NOT NULL, id uuid NOT NULL, kind text NOT NULL DEFAULT 'type' CHECK(kind='type'),
 description text NOT NULL, schema_revision bigint NOT NULL,
 PRIMARY KEY(space_id,id), FOREIGN KEY(space_id,id,kind) REFERENCES memory.entities(space_id,id,kind)
);
CREATE TABLE memory.attribute_defs (
 space_id uuid NOT NULL, id uuid NOT NULL, kind text NOT NULL DEFAULT 'attribute' CHECK(kind='attribute'),
 description text NOT NULL,
 value_type text NOT NULL CHECK(value_type IN ('string','boolean','integer','decimal','timestamp','ref')),
 cardinality text NOT NULL CHECK(cardinality IN ('one','many')),
 unit text NOT NULL DEFAULT '', status text NOT NULL DEFAULT 'active' CHECK(status IN ('active','deprecated')),
 replacement uuid, examples jsonb NOT NULL DEFAULT '[]', not_this jsonb NOT NULL DEFAULT '[]',
 protected boolean NOT NULL DEFAULT false, schema_revision bigint NOT NULL,
 PRIMARY KEY(space_id,id), UNIQUE(space_id,id,value_type,cardinality),
 FOREIGN KEY(space_id,id,kind) REFERENCES memory.entities(space_id,id,kind),
 FOREIGN KEY(space_id,replacement) REFERENCES memory.attribute_defs(space_id,id), CHECK(replacement IS DISTINCT FROM id)
);
CREATE TABLE memory.attribute_domains (
 space_id uuid NOT NULL, attribute_id uuid NOT NULL, type_id uuid NOT NULL,
 PRIMARY KEY(space_id,attribute_id,type_id),
 FOREIGN KEY(space_id,attribute_id) REFERENCES memory.attribute_defs(space_id,id), FOREIGN KEY(space_id,type_id) REFERENCES memory.type_defs(space_id,id)
);
CREATE TABLE memory.attribute_ranges (LIKE memory.attribute_domains INCLUDING ALL);
ALTER TABLE memory.attribute_ranges ADD FOREIGN KEY(space_id,attribute_id) REFERENCES memory.attribute_defs(space_id,id), ADD FOREIGN KEY(space_id,type_id) REFERENCES memory.type_defs(space_id,id);
CREATE TABLE memory.sources (
 space_id uuid NOT NULL, id uuid NOT NULL, kind text NOT NULL DEFAULT 'source' CHECK(kind='source'),
 origin text NOT NULL CHECK(origin IN ('user','document','import','inference','system')),
 uri text NOT NULL DEFAULT '', locator text NOT NULL DEFAULT '', description text NOT NULL CHECK(octet_length(description)<=16384), observed_at timestamptz,
 PRIMARY KEY(space_id,id), FOREIGN KEY(space_id,id,kind) REFERENCES memory.entities(space_id,id,kind)
);
CREATE TABLE memory.transactions (
 space_id uuid NOT NULL REFERENCES memory.spaces(id), revision bigint NOT NULL,
 actor text NOT NULL, client text NOT NULL, request_id uuid NOT NULL, reason text NOT NULL,
 idempotency_key text, request_hash text, result jsonb,
 received_at timestamptz NOT NULL DEFAULT clock_timestamp(), committed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(space_id,revision), UNIQUE(space_id,actor,idempotency_key)
);
CREATE FUNCTION memory.valid_decimal(v numeric) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT length(ltrim(split_part(abs(v)::text,'.',1)||rtrim(split_part(abs(v)::text,'.',2),'0'),'0'))<=38
 AND length(rtrim(split_part(abs(v)::text,'.',2),'0'))<=12
 AND v NOT IN ('NaN'::numeric,'Infinity'::numeric,'-Infinity'::numeric)
$$;
CREATE TABLE memory.current_facts (
 space_id uuid NOT NULL, id uuid NOT NULL, entity_id uuid NOT NULL, attribute_id uuid NOT NULL,
 value_type text NOT NULL, cardinality text NOT NULL,
 value_text text CHECK(octet_length(value_text)<=16384), value_bool boolean, value_integer bigint,
 value_decimal numeric CHECK(memory.valid_decimal(value_decimal)), value_timestamp timestamptz, value_ref uuid,
 value_fingerprint bytea NOT NULL CHECK(octet_length(value_fingerprint)=32), assertion_revision bigint NOT NULL,
 PRIMARY KEY(space_id,id),
 FOREIGN KEY(space_id,entity_id) REFERENCES memory.entities(space_id,id),
 FOREIGN KEY(space_id,attribute_id,value_type,cardinality) REFERENCES memory.attribute_defs(space_id,id,value_type,cardinality),
 FOREIGN KEY(space_id,value_ref) REFERENCES memory.entities(space_id,id),
 FOREIGN KEY(space_id,assertion_revision) REFERENCES memory.transactions(space_id,revision) DEFERRABLE INITIALLY DEFERRED,
 CHECK(num_nonnulls(value_text,value_bool,value_integer,value_decimal,value_timestamp,value_ref)=1),
 CHECK((value_type='string' AND value_text IS NOT NULL) OR (value_type='boolean' AND value_bool IS NOT NULL) OR
       (value_type='integer' AND value_integer IS NOT NULL) OR (value_type='decimal' AND value_decimal IS NOT NULL) OR
       (value_type='timestamp' AND value_timestamp IS NOT NULL) OR (value_type='ref' AND value_ref IS NOT NULL))
);
CREATE UNIQUE INDEX fact_one_value ON memory.current_facts(space_id,entity_id,attribute_id) WHERE cardinality='one';
CREATE UNIQUE INDEX fact_set_membership ON memory.current_facts(space_id,entity_id,attribute_id,value_type,value_fingerprint);
CREATE INDEX fact_entity ON memory.current_facts(space_id,entity_id,attribute_id);
CREATE INDEX fact_reverse ON memory.current_facts(space_id,attribute_id,value_ref,entity_id) WHERE value_ref IS NOT NULL;
CREATE INDEX fact_integer ON memory.current_facts(space_id,attribute_id,value_integer,entity_id) WHERE value_integer IS NOT NULL;
CREATE INDEX fact_decimal ON memory.current_facts(space_id,attribute_id,value_decimal,entity_id) WHERE value_decimal IS NOT NULL;
CREATE INDEX fact_timestamp ON memory.current_facts(space_id,attribute_id,value_timestamp,entity_id) WHERE value_timestamp IS NOT NULL;
CREATE TABLE memory.fact_evidence (
 space_id uuid NOT NULL, fact_id uuid NOT NULL, source_id uuid NOT NULL,
 confidence double precision CHECK(confidence>=0 AND confidence<=1 AND confidence <> 'NaN'::float8), method text NOT NULL DEFAULT '', observed_at timestamptz,
 PRIMARY KEY(space_id,fact_id,source_id), FOREIGN KEY(space_id,fact_id) REFERENCES memory.current_facts(space_id,id) ON DELETE CASCADE,
 FOREIGN KEY(space_id,source_id) REFERENCES memory.sources(space_id,id)
);
CREATE TABLE memory.fact_events (
 space_id uuid NOT NULL, revision bigint NOT NULL, ordinal integer NOT NULL,
 entity_id uuid NOT NULL, kind text NOT NULL, payload jsonb NOT NULL,
 PRIMARY KEY(space_id,revision,ordinal), FOREIGN KEY(space_id,revision) REFERENCES memory.transactions(space_id,revision) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(space_id,entity_id) REFERENCES memory.entities(space_id,id)
);
CREATE INDEX fact_history ON memory.fact_events(space_id,entity_id,revision,ordinal);
CREATE TABLE memory.entity_events (LIKE memory.fact_events INCLUDING ALL);
ALTER TABLE memory.entity_events ADD FOREIGN KEY(space_id,revision) REFERENCES memory.transactions(space_id,revision) DEFERRABLE INITIALLY DEFERRED, ADD FOREIGN KEY(space_id,entity_id) REFERENCES memory.entities(space_id,id);
CREATE TABLE memory.schema_events (LIKE memory.fact_events INCLUDING ALL);
ALTER TABLE memory.schema_events ADD FOREIGN KEY(space_id,revision) REFERENCES memory.transactions(space_id,revision) DEFERRABLE INITIALLY DEFERRED, ADD FOREIGN KEY(space_id,entity_id) REFERENCES memory.entities(space_id,id);
CREATE TABLE memory.schema_proposals (
 space_id uuid NOT NULL REFERENCES memory.spaces(id), id uuid NOT NULL, actor text NOT NULL,
 definition jsonb NOT NULL, schema_revision bigint NOT NULL, status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','applied','rejected')),
 candidates jsonb NOT NULL, rationale text, PRIMARY KEY(space_id,id)
);
CREATE TABLE memory.entity_search (
 space_id uuid NOT NULL, entity_id uuid NOT NULL, text text NOT NULL, tsv tsvector NOT NULL,
 PRIMARY KEY(space_id,entity_id), FOREIGN KEY(space_id,entity_id) REFERENCES memory.entities(space_id,id)
);
CREATE INDEX entity_search_tsv ON memory.entity_search USING gin(tsv);

-- Every schema entity must have a canonical name at commit, including direct SQL.
CREATE FUNCTION memory.require_schema_name() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE sid uuid; eid uuid;
BEGIN
 IF TG_TABLE_NAME='entities' THEN
  IF NEW.kind NOT IN ('attribute','type') THEN RETURN NULL; END IF;
  sid:=NEW.space_id;eid:=NEW.id;
 ELSE sid:=OLD.space_id;eid:=OLD.entity_id; END IF;
 IF EXISTS(SELECT FROM memory.entities e WHERE e.space_id=sid AND e.id=eid AND e.kind IN ('attribute','type') AND NOT EXISTS(SELECT FROM memory.entity_names n WHERE n.space_id=e.space_id AND n.entity_id=e.id AND n.is_canonical)) THEN
 RAISE EXCEPTION 'schema entity requires canonical name' USING ERRCODE='23514'; END IF;
 IF TG_TABLE_NAME='entity_names' AND TG_OP='UPDATE' THEN
 IF EXISTS(SELECT FROM memory.entities e WHERE e.space_id=NEW.space_id AND e.id=NEW.entity_id AND e.kind IN ('attribute','type') AND NOT EXISTS(SELECT FROM memory.entity_names n WHERE n.space_id=e.space_id AND n.entity_id=e.id AND n.is_canonical)) THEN RAISE EXCEPTION 'schema entity requires canonical name' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER entity_schema_name AFTER INSERT OR UPDATE ON memory.entities DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION memory.require_schema_name();
CREATE CONSTRAINT TRIGGER registry_schema_name AFTER DELETE OR UPDATE ON memory.entity_names DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION memory.require_schema_name();
CREATE FUNCTION memory.reject_event_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'immutable event' USING ERRCODE='42501'; END $$;
CREATE TRIGGER immutable_facts BEFORE UPDATE OR DELETE ON memory.fact_events FOR EACH ROW EXECUTE FUNCTION memory.reject_event_mutation();
CREATE TRIGGER immutable_entities BEFORE UPDATE OR DELETE ON memory.entity_events FOR EACH ROW EXECUTE FUNCTION memory.reject_event_mutation();
CREATE TRIGGER immutable_schema BEFORE UPDATE OR DELETE ON memory.schema_events FOR EACH ROW EXECUTE FUNCTION memory.reject_event_mutation();
CREATE FUNCTION memory.protect_fact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM memory.attribute_defs WHERE space_id=NEW.space_id AND id=NEW.attribute_id AND protected) THEN
 RAISE EXCEPTION 'protected metadata' USING ERRCODE='23514'; END IF; RETURN NEW;
END $$;
CREATE TRIGGER protect_fact BEFORE INSERT OR UPDATE ON memory.current_facts FOR EACH ROW EXECUTE FUNCTION memory.protect_fact();

-- One common graph relation. Virtual identities have explicit stable keys and no
-- current_facts rows; all graph operations use this view.
CREATE VIEW memory.read_facts AS
SELECT space_id,id::text AS id,entity_id,attribute_id,value_type,cardinality,value_text,value_bool,value_integer,value_decimal,value_timestamp,value_ref,assertion_revision,false AS virtual FROM memory.current_facts
UNION ALL
SELECT e.space_id,'virtual:'||e.id||':'||a.id||':'||v.key,e.id,a.id,a.value_type,a.cardinality,
 v.txt,NULL::boolean,v.i,NULL::numeric,v.ts,v.ref,e.revision,true
FROM memory.entities e
LEFT JOIN memory.entity_names n ON n.space_id=e.space_id AND n.entity_id=e.id AND n.is_canonical
LEFT JOIN memory.attribute_defs d ON d.space_id=e.space_id AND d.id=e.id
LEFT JOIN memory.type_defs t ON t.space_id=e.space_id AND t.id=e.id
LEFT JOIN memory.sources s ON s.space_id=e.space_id AND s.id=e.id
CROSS JOIN LATERAL (
 VALUES ('entity/id',e.id::text,NULL::bigint,NULL::timestamptz,NULL::uuid,'0'),
 ('entity/ident',n.name,NULL,NULL,NULL,'0'), ('entity/kind',e.kind,NULL,NULL,NULL,'0'),
 ('entity/revision',NULL,e.revision,NULL,NULL,'0'),
 ('schema/value-type',d.value_type,NULL,NULL,NULL,'0'), ('schema/cardinality',d.cardinality,NULL,NULL,NULL,'0'),
 ('schema/description',coalesce(d.description,t.description),NULL,NULL,NULL,'0'), ('schema/unit',nullif(d.unit,''),NULL,NULL,NULL,'0'),
 ('schema/status',d.status,NULL,NULL,NULL,'0'), ('schema/replacement',NULL,NULL,NULL,d.replacement,'0'),
 ('schema/revision',NULL,coalesce(d.schema_revision,t.schema_revision),NULL,NULL,'0'),
 ('source/origin',s.origin,NULL,NULL,NULL,'0'), ('source/uri',nullif(s.uri,''),NULL,NULL,NULL,'0'),
 ('source/description',s.description,NULL,NULL,NULL,'0'), ('source/locator',nullif(s.locator,''),NULL,NULL,NULL,'0'),
 ('source/observed-at',NULL,NULL,s.observed_at,NULL,'0')
 UNION ALL SELECT 'schema/applies-to',NULL,NULL,NULL,type_id,type_id::text FROM memory.attribute_domains WHERE space_id=e.space_id AND attribute_id=e.id
 UNION ALL SELECT 'schema/reference-types',NULL,NULL,NULL,type_id,type_id::text FROM memory.attribute_ranges WHERE space_id=e.space_id AND attribute_id=e.id
 UNION ALL SELECT 'schema/examples',x.value,NULL,NULL,NULL,x.ordinality::text FROM jsonb_array_elements_text(d.examples) WITH ORDINALITY x(value,ordinality)
 UNION ALL SELECT 'schema/not-this',x.value,NULL,NULL,NULL,x.ordinality::text FROM jsonb_array_elements_text(d.not_this) WITH ORDINALITY x(value,ordinality)
) v(name,txt,i,ts,ref,key)
JOIN memory.entity_names an ON an.space_id=e.space_id AND an.name=v.name AND an.is_canonical
JOIN memory.attribute_defs a ON a.space_id=an.space_id AND a.id=an.entity_id
WHERE num_nonnulls(v.txt,v.i,v.ts,v.ref)=1;

CREATE TABLE memory_auth.clients (id text PRIMARY KEY, name text NOT NULL, redirects jsonb NOT NULL, metadata_expires timestamptz, created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE memory_auth.grants (id uuid PRIMARY KEY, client_id text NOT NULL REFERENCES memory_auth.clients(id), resource text NOT NULL, scopes text[] NOT NULL, spaces text[] NOT NULL, revoked boolean NOT NULL DEFAULT false, created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE memory_auth.codes (hash bytea PRIMARY KEY, grant_id uuid NOT NULL REFERENCES memory_auth.grants(id), redirect text NOT NULL, challenge text NOT NULL, expires_at timestamptz NOT NULL);
CREATE TABLE memory_auth.refresh (hash bytea PRIMARY KEY, grant_id uuid NOT NULL REFERENCES memory_auth.grants(id), family uuid NOT NULL, consumed boolean NOT NULL DEFAULT false, expires_at timestamptz NOT NULL);
CREATE INDEX refresh_family ON memory_auth.refresh(family);
CREATE TABLE memory_auth.sessions (hash bytea PRIMARY KEY, csrf text NOT NULL, expires_at timestamptz NOT NULL);
CREATE TABLE memory_auth.login_limits (key text PRIMARY KEY, attempts integer NOT NULL, window_start timestamptz NOT NULL);

-- Internal callers cannot retype an attribute or change an entity's kind.
CREATE FUNCTION memory.freeze_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF (NEW.space_id,NEW.id,NEW.kind) IS DISTINCT FROM (OLD.space_id,OLD.id,OLD.kind) THEN RAISE EXCEPTION 'immutable entity identity' USING ERRCODE='23514'; END IF; RETURN NEW; END $$;
CREATE TRIGGER freeze_entity BEFORE UPDATE ON memory.entities FOR EACH ROW EXECUTE FUNCTION memory.freeze_identity();
CREATE FUNCTION memory.freeze_attribute() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF (NEW.space_id,NEW.id,NEW.value_type,NEW.cardinality,NEW.unit,NEW.protected) IS DISTINCT FROM (OLD.space_id,OLD.id,OLD.value_type,OLD.cardinality,OLD.unit,OLD.protected) OR OLD.protected THEN RAISE EXCEPTION 'immutable attribute validation properties' USING ERRCODE='23514'; END IF; RETURN NEW; END $$;
CREATE TRIGGER freeze_attribute BEFORE UPDATE ON memory.attribute_defs FOR EACH ROW EXECUTE FUNCTION memory.freeze_attribute();
CREATE FUNCTION memory.freeze_name() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF (NEW.space_id,NEW.entity_id,NEW.name) IS DISTINCT FROM (OLD.space_id,OLD.entity_id,OLD.name) THEN RAISE EXCEPTION 'names cannot be reassigned' USING ERRCODE='23514'; END IF; RETURN NEW; END $$;
CREATE TRIGGER freeze_name BEFORE UPDATE ON memory.entity_names FOR EACH ROW EXECUTE FUNCTION memory.freeze_name();
