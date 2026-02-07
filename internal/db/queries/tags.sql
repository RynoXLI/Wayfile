-- name: CreateTag :one
INSERT INTO tags (namespace_id, name, description, path, parent_id, color)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetTagByID :one
SELECT * FROM tags WHERE id = $1;

-- name: GetTagByName :one
SELECT * FROM tags WHERE namespace_id = $1 AND name = $2;

-- name: GetTagByPath :one
SELECT * FROM tags WHERE namespace_id = $1 AND path = $2;

-- name: GetTagsByNamespace :many
SELECT * FROM tags WHERE namespace_id = $1 ORDER BY path;

-- name: UpdateTag :one
UPDATE tags
SET 
    name = COALESCE($2, name),
    description = COALESCE($3, description),
    path = COALESCE($4, path),
    parent_id = COALESCE($5, parent_id),
    color = COALESCE($6, color),
    modified_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteTag :exec
DELETE FROM tags WHERE id = $1;

-- name: GetOrCreateTag :one
INSERT INTO tags (namespace_id, name, path)
SELECT n.id, $2, '/' || $2
FROM namespaces n
WHERE n.name = $1
ON CONFLICT (namespace_id, name) 
DO UPDATE SET name = tags.name
RETURNING *;