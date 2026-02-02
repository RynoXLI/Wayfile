-- name: CreateNamespace :one
INSERT INTO namespaces (name) VALUES ($1) RETURNING *;

-- name: GetNamespaces :many
SELECT * FROM namespaces ORDER BY created_at DESC;

-- name: GetNamespaceByName :one
SELECT * FROM namespaces WHERE name = $1;

-- name: DeleteNamespace :exec
DELETE FROM namespaces WHERE name = $1;