-- name: GetOrCreateTag :one
INSERT INTO tags (namespace_id, name, path)
SELECT n.id, $2, '/' || $2
FROM namespaces n
WHERE n.name = $1
ON CONFLICT (namespace_id, name) 
DO UPDATE SET name = tags.name
RETURNING *;