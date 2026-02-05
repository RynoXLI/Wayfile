-- name: AssociateTag :exec
INSERT INTO document_tags (
    document_id,
    tag_id,
    attributes
) VALUES (
    $1, $2, $3
);

-- name: RemoveTagAssociation :exec
DELETE FROM document_tags
WHERE document_id = $1 AND tag_id = $2;

-- name: GetTagsByDocumentID :many
SELECT dt.tag_id, dt.attributes
FROM document_tags dt
WHERE dt.document_id = $1;

-- name: GetDocumentsByTagID :many
SELECT dt.document_id, dt.attributes
FROM document_tags dt
WHERE dt.tag_id = $1;

-- name: UpdateDocumentTagAttributes :exec
UPDATE document_tags SET
    attributes = $3
WHERE document_id = $1 AND tag_id = $2;