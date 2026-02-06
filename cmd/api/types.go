package main

import "time"

// DocumentResponse represents the response for document upload
type DocumentResponse struct {
	ID          string    `json:"id"              example:"123e4567-e89b-12d3-a456-426614174000"`
	FileName    string    `json:"file_name"       example:"document.pdf"`
	Title       string    `json:"title"           example:"document.pdf"`
	ChecksumSHA string    `json:"checksum_sha256" example:"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"`
	DownloadURL string    `json:"download_url"    example:"http://localhost:8080/api/v1/ns/my-namespace/documents/123e4567-e89b-12d3-a456-426614174000?token=abc.def.123.sig"`
	CreatedAt   time.Time `json:"created_at"      example:"2024-01-15T10:00:00Z"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error string `json:"error" example:"Error message"`
}
