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