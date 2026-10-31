-- Operator-run cluster bootstrap. No passwords or OS peer mappings are embedded.
DO $$ BEGIN IF NOT EXISTS(SELECT FROM pg_roles WHERE rolname='memory_owner') THEN CREATE ROLE memory_owner NOLOGIN; END IF; END $$;
DO $$ BEGIN IF NOT EXISTS(SELECT FROM pg_roles WHERE rolname='memory_migrator') THEN CREATE ROLE memory_migrator LOGIN; END IF; END $$;
DO $$ BEGIN IF NOT EXISTS(SELECT FROM pg_roles WHERE rolname='memory_app') THEN CREATE ROLE memory_app LOGIN; END IF; END $$;
DO $$ BEGIN IF NOT EXISTS(SELECT FROM pg_roles WHERE rolname='memory_reader') THEN CREATE ROLE memory_reader LOGIN; END IF; END $$;
GRANT memory_owner TO memory_migrator;

DO $$ BEGIN EXECUTE format('GRANT CREATE ON DATABASE %I TO memory_owner',current_database()); EXECUTE format('REVOKE TEMPORARY,CREATE ON DATABASE %I FROM PUBLIC',current_database()); END $$;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
