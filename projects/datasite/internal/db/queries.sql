-- name: GetCinema :one
SELECT * FROM cinemas WHERE id = ?;

-- name: ListCinemas :many
SELECT * FROM cinemas ORDER BY name;

-- name: CreateCinema :one
INSERT INTO cinemas(name) VALUES (?) RETURNING *;

-- name: GetMovieLog :one
SELECT * FROM movie_logs WHERE id = ?;

-- name: GetMovieLogsForCinema :many
SELECT * FROM movie_logs WHERE cinema_id = ?;

-- name: GetLogsForMovie :many
SELECT * FROM movie_logs WHERE movie_id = ?;

-- name: LogMovie :one
INSERT INTO
    movie_logs(movie_id, date, cinema_id, rating, review, review_contains_spoilers)
    VALUES (?, ?, ?, ?, ?, ?)
    RETURNING *;

-- name: EditMovieLog :one
UPDATE movie_logs
    SET movie_id = ?, date = ?, cinema_id = ?, rating = ?, review = ?, review_contains_spoilers = ?
    WHERE id = ?
    RETURNING *;

-- name: UpdateCinema :one
UPDATE cinemas
    SET name = ?
    WHERE id = ?
    RETURNING *;

-- name: DeleteMovieLog :exec
DELETE FROM movie_logs WHERE id = ?;

-- name: DeleteCinema :exec
DELETE FROM cinemas WHERE id = ?;

-- name: GetRecentMovieLogs :many
SELECT * FROM movie_logs ORDER BY date DESC LIMIT ?;

-- name: ListMovieLogs :many
SELECT * FROM movie_logs ORDER BY date DESC;
