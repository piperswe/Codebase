-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetRole :one
SELECT * FROM roles WHERE id = $1;

-- name: GetRolePermissions :many
SELECT permission FROM permission_assignments WHERE role_id = $1;

-- name: GetUserRoles :many
SELECT role_id FROM role_assignments WHERE user_id = $1;

-- name: GetUserPermissions :many
SELECT DISTINCT permission
FROM permission_assignments
INNER JOIN role_assignments ON permission_assignments.role_id = role_assignments.role_id
WHERE role_assignments.user_id = $1;

-- name: UserHasPermission :one
SELECT 1
FROM permission_assignments
INNER JOIN role_assignments ON permission_assignments.role_id = role_assignments.role_id
WHERE role_assignments.user_id = $1 AND permission_assignments.permission = $2
LIMIT 1;