ALTER SCHEMA memory OWNER TO memory_owner;
ALTER SCHEMA memory_auth OWNER TO memory_owner;
DO $$ DECLARE r record; BEGIN
 FOR r IN SELECT tablename FROM pg_tables WHERE schemaname IN ('memory','memory_auth') LOOP
  -- Both schemas have disjoint table names except none in V1.
  EXECUTE format('ALTER TABLE %I.%I OWNER TO memory_owner', (SELECT schemaname FROM pg_tables WHERE tablename=r.tablename AND schemaname IN ('memory','memory_auth') LIMIT 1),r.tablename);
 END LOOP;
END $$;
ALTER VIEW memory.read_facts OWNER TO memory_owner;
REVOKE ALL ON SCHEMA memory,memory_auth FROM PUBLIC;
GRANT USAGE ON SCHEMA memory,memory_auth TO memory_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA memory,memory_auth TO memory_app;
REVOKE UPDATE,DELETE ON memory.fact_events,memory.entity_events,memory.schema_events FROM memory_app;
REVOKE ALL ON memory.read_facts FROM memory_app;
GRANT SELECT ON memory.read_facts TO memory_app;
GRANT USAGE ON SCHEMA memory TO memory_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA memory TO memory_reader;
REVOKE ALL ON memory.schema_proposals,memory.transactions FROM memory_reader;
-- History is exposed without idempotency response bodies or request hashes.
CREATE OR REPLACE VIEW memory.transaction_history AS SELECT space_id,revision,actor,client,reason,committed_at FROM memory.transactions;
ALTER VIEW memory.transaction_history OWNER TO memory_owner;
GRANT SELECT ON memory.transaction_history TO memory_reader,memory_app;
ALTER DEFAULT PRIVILEGES FOR ROLE memory_owner IN SCHEMA memory GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO memory_app;
ALTER DEFAULT PRIVILEGES FOR ROLE memory_owner IN SCHEMA memory_auth GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO memory_app;
-- Reader grants on future relations require explicit migration review.
REVOKE DELETE ON memory.entities,memory.entity_names,memory.attribute_defs,memory.type_defs FROM memory_app;
REVOKE UPDATE,DELETE ON memory.sources,memory.transactions,memory.attribute_domains,memory.attribute_ranges FROM memory_app;
REVOKE ALL ON memory.transaction_history FROM memory_app;
GRANT SELECT ON memory.transaction_history TO memory_app;
