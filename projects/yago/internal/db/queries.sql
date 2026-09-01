-- name: GetUser :one
SELECT * FROM users WHERE id = ?;
