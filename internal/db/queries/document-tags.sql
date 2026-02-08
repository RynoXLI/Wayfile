-- name: AddDocumentTag :exec
INSERT INTO document_tags (document_id, tag_id, attributes, attributes_metadata)
VALUES ($1, $2, $3, $4)
ON CONFLICT (document_id, tag_id) DO UPDATE
SET attributes = EXCLUDED.attributes,
    attributes_metadata = EXCLUDED.attributes_metadata,
    modified_at = NOW();

-- name: RemoveDocumentTag :exec
DELETE FROM document_tags
WHERE document_id = $1 AND tag_id = $2;

-- name: GetDocumentTagsWithAttributes :many
SELECT t.id, t.namespace_id, t.name, t.path, dt.attributes, dt.attributes_metadata, dt.modified_at
FROM tags t
JOIN document_tags dt ON t.id = dt.tag_id
WHERE dt.document_id = $1;

----------- Tag-specific attributes -----------

-- name: GetDocumentTagAttributes :one
SELECT attributes, attributes_metadata
FROM document_tags
WHERE document_id = $1 AND tag_id = $2;

-- name: UpdateDocumentTagAttributes :exec
UPDATE document_tags
SET attributes = $3, attributes_metadata = $4, modified_at = NOW()
WHERE document_id = $1 AND tag_id = $2;