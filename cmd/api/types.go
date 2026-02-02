package main

import "time"

// DocumentResponse represents the response for document upload
type DocumentResponse struct {
	ID           string    `json:"id"                      example:"123e4567-e89b-12d3-a456-426614174000"`
	NamespaceID  string    `json:"namespace_id"            example:"123e4567-e89b-12d3-a456-426614174001"`
	FileName     string    `json:"file_name"               example:"document.pdf"`
	Title        string    `json:"title"                   example:"document.pdf"`
	DocumentDate *string   `json:"document_date,omitempty" example:"2024-01-15"`
	MimeType     string    `json:"mime_type"               example:"application/pdf"`
	ChecksumSHA  string    `json:"checksum_sha256"         example:"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"`
	FileSize     int64     `json:"file_size"               example:"102400"`
	PageCount    *int32    `json:"page_count,omitempty"    example:"10"`
	CreatedAt    time.Time `json:"created_at"              example:"2024-01-15T10:00:00Z"`
	ModifiedAt   time.Time `json:"modified_at"             example:"2024-01-15T10:00:00Z"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error string `json:"error" example:"Error message"`
}
