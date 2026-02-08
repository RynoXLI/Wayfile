-- name: CreateDocument :one
INSERT INTO documents (
    id,
    namespace_id,
    file_name,
    title,
    mime_type,
    checksum_sha256,
    file_size
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
) RETURNING 
    id,
    file_name,
    title,
    checksum_sha256,
    created_at;

-- name: GetDocumentByID :one
SELECT * FROM documents WHERE id = $1;

-- name: DeleteDocument :exec
DELETE FROM documents WHERE id = $1;

-- name: UpdateDocument :one
UPDATE documents SET
    file_name = COALESCE($2, file_name),
    title = COALESCE($3, title),
    document_date = COALESCE($4, document_date),
    mime_type = COALESCE($5, mime_type),
    file_size = COALESCE($6, file_size),
    attributes = COALESCE($7, attributes),
    attributes_metadata = COALESCE($8, attributes_metadata),
    modified_at = NOW()
WHERE id = $1
RETURNING *;