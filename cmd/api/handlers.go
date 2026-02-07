package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/RynoXLI/Wayfile/internal/auth"
	"github.com/RynoXLI/Wayfile/internal/storage"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// HealthOutput is the health check response
type HealthOutput struct {
	Body struct {
		Status string `json:"status" example:"ok" doc:"Service status"`
	}
}

// DocumentUploadInput handles file upload
type DocumentUploadInput struct {
	Namespace string `path:"namespace" maxLength:"255" doc:"Namespace name"`
	RawBody   huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" required:"true" doc:"File to upload"`
	}]
}

// DocumentUploadOutput is the upload response
type DocumentUploadOutput struct {
	Status int `header:"Status-Code"`
	Body   DocumentResponse
}

// DocumentResponse represents the response for document operations
type DocumentResponse struct {
	ID          string    `json:"id"              example:"123e4567-e89b-12d3-a456-426614174000"                                                                              doc:"Document UUID"`
	FileName    string    `json:"file_name"       example:"document.pdf"                                                                                                      doc:"Original filename"`
	Title       string    `json:"title"           example:"document.pdf"                                                                                                      doc:"Document title"`
	ChecksumSHA string    `json:"checksum_sha256" example:"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"                                                  doc:"SHA-256 checksum"`
	DownloadURL string    `json:"download_url"    example:"http://localhost:8080/api/v1/ns/my-namespace/documents/123e4567-e89b-12d3-a456-426614174000?token=abc.def.123.sig" doc:"Pre-signed download URL"`
	CreatedAt   time.Time `json:"created_at"      example:"2024-01-15T10:00:00Z"                                                                                              doc:"Creation timestamp"`
}

// DocumentDownloadInput handles download requests
type DocumentDownloadInput struct {
	Namespace  string `path:"namespace"  maxLength:"255" doc:"Namespace name"`
	DocumentID string `path:"documentID"                 doc:"Document UUID"                       format:"uuid"`
	Token      string `                                  doc:"Pre-signed token for authentication"               query:"token" required:"false"`
}

// RegisterRoutes registers all Huma operations
func RegisterRoutes(api huma.API, app *App) {
	// Health check
	huma.Register(api, huma.Operation{
		OperationID: "get-health",
		Method:      "GET",
		Path:        "/health",
		Summary:     "Health check",
		Description: "Check if the API server is running and dependencies are healthy",
		Tags:        []string{"health"},
	}, func(ctx context.Context, _ *struct{}) (*HealthOutput, error) {
		healthCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		// Check database
		if err := app.pool.Ping(healthCtx); err != nil {
			app.logger.Error("Health check failed: database", "error", err)
			return nil, huma.Error503ServiceUnavailable("Database unavailable")
		}

		// Check NATS
		if !app.nc.IsConnected() {
			app.logger.Error("Health check failed: NATS disconnected")
			return nil, huma.Error503ServiceUnavailable("Message queue unavailable")
		}

		resp := &HealthOutput{}
		resp.Body.Status = "ok"
		return resp, nil
	})

	// Upload document
	huma.Register(api, huma.Operation{
		OperationID: "upload-document",
		Method:      "POST",
		Path:        "/api/v1/ns/{namespace}/documents",
		Summary:     "Upload a document",
		Description: "Upload a file to the specified namespace",
		Tags:        []string{"documents"},
	}, func(ctx context.Context, input *DocumentUploadInput) (*DocumentUploadOutput, error) {
		// Get file data from multipart form
		formData := input.RawBody.Data()

		// Get file metadata
		filename := formData.File.Filename
		size := formData.File.Size
		contentType := formData.File.ContentType

		// Upload the file using the document service
		result, err := app.documentService.UploadDocument(
			ctx,
			input.Namespace,
			filename,
			contentType,
			int(size),
			formData.File,
		)
		if err != nil {
			if errors.Is(err, storage.ErrDuplicateFile) {
				return nil, huma.Error409Conflict("File with this content already exists")
			}
			if errors.Is(err, storage.ErrNotFound) {
				return nil, huma.Error404NotFound("Namespace not found")
			}
			app.logger.Error(
				"Failed to upload file",
				"error", err,
				"namespace", input.Namespace,
				"filename", filename,
			)
			return nil, huma.Error500InternalServerError("Error uploading the file")
		}

		// Create response with download URL
		resp := &DocumentUploadOutput{}
		resp.Status = 201
		resp.Body = DocumentResponse{
			ID:          result.Document.ID.String(),
			FileName:    result.Document.FileName,
			Title:       result.Document.Title,
			ChecksumSHA: result.Document.ChecksumSha256,
			DownloadURL: result.DownloadURL,
			CreatedAt:   result.Document.CreatedAt.Time,
		}

		return resp, nil
	})

	// Download document
	huma.Register(api, huma.Operation{
		OperationID: "download-document",
		Method:      "GET",
		Path:        "/api/v1/ns/{namespace}/documents/{documentID}",
		Summary:     "Download a document",
		Description: "Download a file from the specified namespace",
		Tags:        []string{"documents"},
	}, func(ctx context.Context, input *DocumentDownloadInput) (*huma.StreamResponse, error) {
		// Validate UUID
		if _, err := uuid.Parse(input.DocumentID); err != nil {
			return nil, huma.Error404NotFound("Invalid document ID")
		}

		// Verify token if provided
		if input.Token != "" {
			ns, docID, err := app.signer.VerifyToken(input.Token)
			if err != nil {
				if errors.Is(err, auth.ErrTokenExpired) {
					return nil, huma.Error401Unauthorized("Token expired")
				}
				return nil, huma.Error401Unauthorized("Invalid token")
			}
			// Verify token is for the correct resource
			if ns != input.Namespace || docID != input.DocumentID {
				return nil, huma.Error401Unauthorized("Token not valid for this resource")
			}
		}

		// Download the file
		file, doc, err := app.documentService.DownloadDocument(
			ctx,
			input.Namespace,
			input.DocumentID,
		)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return nil, huma.Error404NotFound("File not found")
			}
			app.logger.Error("Failed to download file", "error", err)
			return nil, huma.Error500InternalServerError("Error downloading the file")
		}

		// Return streaming response
		return &huma.StreamResponse{
			Body: func(ctx huma.Context) {
				defer func() { _ = file.Close() }()
				ctx.SetHeader(
					"Content-Disposition",
					fmt.Sprintf("attachment; filename=%q", doc.FileName),
				)
				ctx.SetHeader("Content-Type", doc.MimeType)
				if _, err := io.Copy(ctx.BodyWriter(), file); err != nil {
					app.logger.Error("Failed to stream file", "error", err)
				}
			},
		}, nil
	})
}
