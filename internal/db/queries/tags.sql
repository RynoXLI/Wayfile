-- name: CreateTag :one
INSERT INTO tags (
    namespace_id,
    name,
    description,
    path,
    parent_id,
    color
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: GetTagByID :one
SELECT * FROM tags WHERE id = $1;

-- name: GetTagsByNamespace :many
SELECT * FROM tags 
WHERE namespace_id = $1 
ORDER BY created_at DESC 
LIMIT $2 OFFSET $3;

-- name: DeleteTag :exec
DELETE FROM tags WHERE id = $1;

-- name: UpdateTag :one
UPDATE tags SET
    name = COALESCE($2, name),
    description = COALESCE($3, description),
    path = COALESCE($4, path),
    parent_id = COALESCE($5, parent_id),
    color = COALESCE($6, color),
    modified_at = NOW()
WHERE id = $1
RETURNING *;

-- name: GetTagsByParentID :many
SELECT * FROM tags
WHERE parent_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: GetTags :many
SELECT * FROM tags
WHERE namespace_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;