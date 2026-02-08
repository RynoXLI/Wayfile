// Package services contains business logic and orchestration
package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"

	eventsv1 "github.com/RynoXLI/Wayfile/gen/go/events/v1"
	"github.com/RynoXLI/Wayfile/internal/auth"
	"github.com/RynoXLI/Wayfile/internal/db/sqlc"
	"github.com/RynoXLI/Wayfile/internal/events"
	"github.com/RynoXLI/Wayfile/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DocumentService orchestrates document operations across storage, events, and URL generation
type DocumentService struct {
	storage    *storage.Storage
	publisher  events.Publisher
	signer     *auth.Signer
	baseURL    string
	queries    *sqlc.Queries
	tagService *TagService
}

// ExtractionMethod represents how a tag or attribute was extracted
type ExtractionMethod string

const (
	// ExtractionMethodManual indicates the tag/attribute was manually set by a user
	ExtractionMethodManual ExtractionMethod = "manual"
	// ExtractionMethodAutomatic indicates the tag/attribute was automatically extracted
	ExtractionMethodAutomatic ExtractionMethod = "automatic"
)

// AttributeExtractionInfo contains extraction info for a single attribute
type AttributeExtractionInfo struct {
	Method      ExtractionMethod `json:"extraction_method"`
	ExtractedBy string           `json:"extracted_by"`
	ExtractedAt time.Time        `json:"extracted_at"`
	Source      string           `json:"source"`
	Confidence  *float64         `json:"confidence,omitempty"`
}

// DocumentTagMetadata contains comprehensive extraction information
type DocumentTagMetadata struct {
	// Tag tracks when/how the tag association was created
	Tag AttributeExtractionInfo `json:"tag"`
	// Attributes tracks extraction info for each attribute field
	Attributes map[string]AttributeExtractionInfo `json:"attributes,omitempty"`
}

// NewDocumentService creates a new document service with the given dependencies
func NewDocumentService(
	storage *storage.Storage,
	publisher events.Publisher,
	signer *auth.Signer,
	baseURL string,
	queries *sqlc.Queries,
	tagService *TagService,
) *DocumentService {
	return &DocumentService{
		storage:    storage,
		publisher:  publisher,
		signer:     signer,
		baseURL:    baseURL,
		queries:    queries,
		tagService: tagService,
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
	extractionMethod ExtractionMethod,
	extractedBy string,
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
		// Parse attributes JSON to validate it's proper JSON
		var attributesMap map[string]interface{}
		if err := json.Unmarshal([]byte(*attributesJSON), &attributesMap); err != nil {
			return status.Errorf(codes.InvalidArgument, "invalid attributes JSON: %v", err)
		}

		// Validate attributes against tag's schema using TagService
		if err := s.tagService.ValidateAttributes(ctx, tag.ID, attributesMap); err != nil {
			return status.Errorf(codes.InvalidArgument, "attribute validation failed: %v", err)
		}

		attributesData = []byte(*attributesJSON)
	}

	// Convert document ID to pgtype.UUID
	docPgUUID := pgtype.UUID{Bytes: docUUID, Valid: true}

	// Create comprehensive extraction metadata
	now := time.Now()
	tagInfo := AttributeExtractionInfo{
		Method:      extractionMethod,
		ExtractedBy: extractedBy,
		ExtractedAt: now,
		Source:      string(extractionMethod),
	}

	metadata := DocumentTagMetadata{
		Tag: tagInfo,
	}

	// If attributes are provided, create extraction info for each attribute field
	if attributesJSON != nil && *attributesJSON != "" {
		var attributesMap map[string]interface{}
		if err := json.Unmarshal([]byte(*attributesJSON), &attributesMap); err != nil {
			// This should never happen as we validated the JSON above
			return status.Errorf(codes.Internal, "failed to parse validated JSON: %v", err)
		}

		metadata.Attributes = make(map[string]AttributeExtractionInfo)
		for fieldName := range attributesMap {
			metadata.Attributes[fieldName] = AttributeExtractionInfo{
				Method:      extractionMethod,
				ExtractedBy: extractedBy,
				ExtractedAt: now,
				Source:      string(extractionMethod),
			}
		}
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to marshal metadata: %v", err)
	}

	// Add the document-tag association
	err = s.queries.AddDocumentTag(ctx, docPgUUID, tag.ID, attributesData, metadataJSON)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to add tag to document: %v", err)
	}

	// Publish tag extracted event
	attributesStr := ""
	if attributesJSON != nil {
		attributesStr = *attributesJSON
	}

	// Include extraction method in event metadata
	eventMetadata := fmt.Sprintf(
		`{"extraction_method":"%s","extracted_by":"%s"}`,
		extractionMethod,
		extractedBy,
	)
	event := &eventsv1.TagExtractedEvent{
		DocumentId: documentID,
		Namespace:  namespace,
		TagPath:    tagPath,
		Metadata:   eventMetadata,
		Attributes: attributesStr,
	}
	if err := s.publisher.TagExtracted(event); err != nil {
		// Log error but don't fail the operation
		// The tag association was successful, event publishing is secondary
		slog.Error(
			"failed to publish TagExtracted event",
			"error",
			err,
			"document_id",
			documentID,
			"tag_path",
			tagPath,
		)
		return nil
	}

	return nil
}

// UpdateAttributeExtractionInfo updates extraction info for specific attribute fields
// This allows tracking when individual attributes are modified by different methods
func (s *DocumentService) UpdateAttributeExtractionInfo(
	ctx context.Context,
	namespace string,
	documentID string,
	tagPath string,
	attributeUpdates map[string]AttributeExtractionInfo,
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

	docPgUUID := pgtype.UUID{Bytes: docUUID, Valid: true}

	// Get current metadata
	currentData, err := s.queries.GetDocumentTagAttributes(ctx, docPgUUID, tag.ID)
	if err != nil {
		return status.Errorf(codes.NotFound, "document-tag association not found: %v", err)
	}

	// Parse existing metadata
	var metadata DocumentTagMetadata
	if len(currentData.AttributesMetadata) > 0 {
		if err := json.Unmarshal(currentData.AttributesMetadata, &metadata); err != nil {
			// If we can't parse existing metadata, create new structure
			metadata = DocumentTagMetadata{
				Tag: AttributeExtractionInfo{
					Method:      ExtractionMethodManual,
					ExtractedBy: "unknown",
					ExtractedAt: time.Now(),
					Source:      "manual",
				},
			}
		}
	}

	// Initialize attributes map if needed
	if metadata.Attributes == nil {
		metadata.Attributes = make(map[string]AttributeExtractionInfo)
	}

	// Update specific attribute extraction info
	for fieldName, extractionInfo := range attributeUpdates {
		metadata.Attributes[fieldName] = extractionInfo
	}

	// Marshal updated metadata
	updatedMetadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to marshal updated metadata: %v", err)
	}

	// Update the metadata in the database (keeping existing attributes)
	err = s.queries.UpdateDocumentTagAttributes(
		ctx,
		docPgUUID,
		tag.ID,
		currentData.Attributes,
		updatedMetadataJSON,
	)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to update attribute metadata: %v", err)
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
