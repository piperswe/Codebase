-- name: GetUserCount :one
SELECT COUNT(*) FROM users;

-- name: GetUser :one
SELECT * FROM users WHERE id = @user_id;

-- name: CreateUserWithUlid :one
INSERT INTO users (ulid, email_address, password_hash)
VALUES (@ulid, @email_address, @password_hash)
RETURNING *;

-- name: UpdateUser :one
UPDATE users
SET email_address = @email_address,
    password_hash = @password_hash
WHERE id = @user_id
RETURNING *;

-- name: GetProfile :one
SELECT * FROM profiles WHERE id = @profile_id;

-- name: CreateProfileWithUlid :one
INSERT INTO profiles (ulid, owner_id, username, display_name)
VALUES (@ulid, @owner_id, @username, @display_name)
RETURNING *;

-- name: UpdateProfile :one
UPDATE profiles
SET username = @username,
    display_name = @display_name
WHERE id = @profile_id
RETURNING *;

-- name: GetRole :one
SELECT * FROM roles WHERE id = @role_id;

-- name: GetRolePermissions :many
SELECT permission FROM permission_assignments WHERE role_id = @role_id;

-- name: GetUserRoles :many
SELECT role_id FROM role_assignments WHERE user_id = @user_id;

-- name: GetUserProfiles :many
SELECT * FROM profiles WHERE owner_id = @user_id;

-- name: GetUserPermissions :many
SELECT DISTINCT permission
FROM permission_assignments
INNER JOIN role_assignments ON permission_assignments.role_id = role_assignments.role_id
WHERE role_assignments.user_id = @user_id;

-- name: UserHasPermission :one
SELECT COUNT(*) > 0
FROM permission_assignments
INNER JOIN role_assignments ON permission_assignments.role_id = role_assignments.role_id
WHERE role_assignments.user_id = @user_id AND permission_assignments.permission = @permission;

-- name: UserHasOneOfPermissions :one
SELECT COUNT(*) > 0
FROM permission_assignments
INNER JOIN role_assignments ON permission_assignments.role_id = role_assignments.role_id
WHERE role_assignments.user_id = @user_id AND permission_assignments.permission = ANY(@permissions::text[]);