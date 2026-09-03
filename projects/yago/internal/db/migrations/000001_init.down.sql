DROP INDEX IF EXISTS circle_membership_by_profile_id;
DROP INDEX IF EXISTS circle_membership_by_circle_id;
DROP TABLE IF EXISTS circle_membership;

DROP INDEX IF EXISTS circle_by_user_id_and_name;
DROP INDEX IF EXISTS circle_by_user_id;
DROP INDEX IF EXISTS circle_by_ulid;
DROP TABLE IF EXISTS circle;

DROP INDEX IF EXISTS friendships_by_addressee_id;
DROP INDEX IF EXISTS friendships_by_requester_id;
DROP TABLE IF EXISTS friendships;

DROP TYPE IF EXISTS friendship_status;

DROP INDEX IF EXISTS profiles_by_username;
DROP INDEX IF EXISTS profiles_by_owner_id;
DROP INDEX IF EXISTS profiles_by_ulid;
DROP TABLE IF EXISTS profiles;

DROP INDEX IF EXISTS role_assignments_by_role_id;
DROP INDEX IF EXISTS role_assignments_by_user_id;
DROP TABLE IF EXISTS role_assignments;

DROP INDEX IF EXISTS permission_assignments_by_role_id;
DROP TABLE IF EXISTS permission_assignments;

DROP TABLE IF EXISTS roles;

DROP INDEX IF EXISTS users_by_email_address;
DROP INDEX IF EXISTS users_by_ulid;
DROP TABLE IF EXISTS users;
