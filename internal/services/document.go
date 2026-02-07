// Package services contains business logic and orchestration
package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	eventsv1 "github.com/RynoXLI/Wayfile/gen/go/events/v1"
	"github.com/RynoXLI/Wayfile/internal/auth"
	"github.com/RynoXLI/Wayfile/internal/db/sqlc"
	"github.com/RynoXLI/Wayfile/internal/events"
	"github.com/RynoXLI/Wayfile/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DocumentService orchestrates document operations across storage, events, and URL generation
type DocumentService struct {
	storage   *storage.Storage
	publisher events.Publisher
	signer    *auth.Signer
	baseURL   string
	queries   *sqlc.Queries
}

// NewDocumentService creates a new document service with the given dependencies
func NewDocumentService(
	storage *storage.Storage,
	publisher events.Publisher,
	signer *auth.Signer,
	baseURL string,
	queries *sqlc.Queries,
) *DocumentService {
	return &DocumentService{
		storage:   storage,
		publisher: publisher,
		signer:    signer,
		baseURL:   baseURL,
		queries:   queries,
	}
}

// DocumentUploadResult contains the uploaded document and its pre-signed download URL
type DocumentUploadResult struct {
	Document    *sqlc.CreateDocumentRow
	DownloadURL string
}

// UploadDocument uploads a document, generates a download URL, and publishes an event
func (s *DocumentService) UploadDocument(
	ctx context.Context,
	namespace string,
	filename string,
	mimeType string,
	fileSize int,
	data io.Reader,
) (*DocumentUploadResult, error) {
	result, err := s.storage.Upload(ctx, namespace, filename, mimeType, fileSize, data)
	if err != nil {
		return nil, err
	}

	docID := result.Document.ID.String()
	token := s.signer.GenerateToken(result.NamespaceID, docID, 24*time.Hour)
	downloadURL := fmt.Sprintf("%s/api/v1/ns/%s/documents/%s?token=%s",
		s.baseURL, namespace, docID, token)

	event := &eventsv1.DocumentUploadedEvent{
		DocumentId:  docID,
		NamespaceId: result.NamespaceID,
		Filename:    filename,
		MimeType:    mimeType,
	}
	if err := s.publisher.DocumentUploaded(event); err != nil {
		return nil, err
	}

	return &DocumentUploadResult{
		Document:    result.Document,
		DownloadURL: downloadURL,
	}, nil
}

// DownloadDocument retrieves a document from storage
func (s *DocumentService) DownloadDocument(
	ctx context.Context,
	namespace string,
	documentID string,
) (io.ReadCloser, *sqlc.Document, error) {
	return s.storage.Download(ctx, namespace, documentID)
}

// DeleteDocument removes a document from storage
func (s *DocumentService) DeleteDocument(
	ctx context.Context,
	namespace string,
	documentID string,
) error {
	return s.storage.Delete(ctx, namespace, documentID)
}

// AddTagToDocument associates a tag with a document and validates attributes against the schema
func (s *DocumentService) AddTagToDocument(
	ctx context.Context,
	namespace string,
	documentID string,
	tagPath string,
	attributesJSON *string,
) error {
	// Get namespace ID
	ns, err := s.queries.GetNamespaceByName(ctx, namespace)
	if err != nil {
		return status.Errorf(codes.NotFound, "namespace not found: %v", err)
	}

	// Get tag by path
	tag, err := s.queries.GetTagByPath(ctx, ns.ID, tagPath)
	if err != nil {
		return status.Errorf(codes.NotFound, "tag not found at path %q: %v", tagPath, err)
	}

	// Parse document ID
	docUUID, err := uuid.Parse(documentID)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid document ID: %v", err)
	}

	// Validate attributes if provided
	var attributesData []byte
	if attributesJSON != nil && *attributesJSON != "" {
		// Get the tag's attribute schema
		schema, err := s.queries.GetLatestSchemaByTagID(ctx, tag.ID)
		if err == nil {
			// Schema exists, validate the attributes
			if err := s.validateAttributes(*attributesJSON, schema.JsonSchema); err != nil {
				return status.Errorf(codes.InvalidArgument, "attribute validation failed: %v", err)
			}
		}
		// If no schema exists, we still store the attributes (schema-optional)
		attributesData = []byte(*attributesJSON)
	}

	// Convert document ID to pgtype.UUID
	docPgUUID := pgtype.UUID{Bytes: docUUID, Valid: true}

	// Add the document-tag association
	err = s.queries.AddDocumentTag(ctx, docPgUUID, tag.ID, attributesData)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to add tag to document: %v", err)
	}

	return nil
}

// RemoveTagFromDocument removes a tag association from a document
func (s *DocumentService) RemoveTagFromDocument(
	ctx context.Context,
	namespace string,
	documentID string,
	tagPath string,
) error {
	// Get namespace ID
	ns, err := s.queries.GetNamespaceByName(ctx, namespace)
	if err != nil {
		return status.Errorf(codes.NotFound, "namespace not found: %v", err)
	}

	// Get tag by path
	tag, err := s.queries.GetTagByPath(ctx, ns.ID, tagPath)
	if err != nil {
		return status.Errorf(codes.NotFound, "tag not found at path %q: %v", tagPath, err)
	}

	// Parse document ID
	docUUID, err := uuid.Parse(documentID)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid document ID: %v", err)
	}

	// Convert document ID to pgtype.UUID
	docPgUUID := pgtype.UUID{Bytes: docUUID, Valid: true}

	// Remove the document-tag association
	err = s.queries.RemoveDocumentTag(ctx, docPgUUID, tag.ID)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to remove tag from document: %v", err)
	}

	return nil
}

// validateAttributes validates attributes JSON against a JSON schema
func (s *DocumentService) validateAttributes(attributesJSON string, schemaJSON []byte) error {
	// Parse the schema
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020

	if err := compiler.AddResource("schema.json", bytes.NewReader(schemaJSON)); err != nil {
		return fmt.Errorf("failed to add schema resource: %w", err)
	}

	schema, err := compiler.Compile("schema.json")
	if err != nil {
		return fmt.Errorf("failed to compile schema: %w", err)
	}

	// Parse the attributes JSON
	var attributes interface{}
	if err := json.Unmarshal([]byte(attributesJSON), &attributes); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	// Validate
	if err := schema.Validate(attributes); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	return nil
}
