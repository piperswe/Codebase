DROP INDEX IF EXISTS role_assignments_by_role_id;
DROP INDEX IF EXISTS role_assignments_by_user_id;
DROP TABLE IF EXISTS role_assignments;

DROP INDEX IF EXISTS permission_assignments_by_role_id;
DROP TABLE IF EXISTS permission_assignments;

DROP TABLE IF EXISTS roles;

DROP INDEX IF EXISTS users_by_email_address;
DROP TABLE IF EXISTS users;