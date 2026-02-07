-- name: GetLatestSchemaByTagID :one
SELECT 
    tag_id, 
    version,
    json_schema,
    created_at
FROM attribute_schemas
WHERE tag_id = $1
ORDER BY version DESC
LIMIT 1;

-- name: GetSchemas :many
WITH LATEST AS (
    SELECT tag_id, max(version) as latest_version 
    FROM attribute_schemas
    GROUP BY tag_id
)
SELECT 
    a.tag_id, 
    a.version,
    a.json_schema,
    a.created_at
FROM attribute_schemas a
JOIN LATEST l ON 
    a.tag_id = l.tag_id AND 
    a.version = l.latest_version;

-- name: CreateSchema :one
INSERT INTO attribute_schemas (
    tag_id,
    json_schema
) VALUES (
    $1, $2
) RETURNING 
    tag_id,
    version,
    json_schema,
    created_at;