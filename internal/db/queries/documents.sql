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

-- name: GetDocumentsByNamespace :many
SELECT * FROM documents 
WHERE namespace_id = $1 
ORDER BY created_at DESC 
LIMIT $2 OFFSET $3;

-- name: DeleteDocument :exec
DELETE FROM documents WHERE id = $1;

-- name: GetDocumentsByChecksum :many
SELECT * FROM documents
WHERE checksum_sha256 = $1
ORDER BY created_at DESC;

-- name: UpdateDocument :one
UPDATE documents SET
    file_name = COALESCE($2, file_name),
    title = COALESCE($3, title),
    document_date = COALESCE($4, document_date),
    mime_type = COALESCE($5, mime_type),
    file_size = COALESCE($6, file_size),
    attributes = COALESCE($7, attributes),
    modified_at = NOW()
WHERE id = $1
RETURNING *;