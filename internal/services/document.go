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

// Helper functions for common operations

// validateNamespace validates and retrieves a namespace by name
func (s *DocumentService) validateNamespace(
	ctx context.Context,
	namespace string,
) (*sqlc.Namespace, error) {
	ns, err := s.queries.GetNamespaceByName(ctx, namespace)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "namespace not found: %v", err)
	}
	return &ns, nil
}

// parseAndValidateDocumentID parses a document ID string and converts it to pgtype.UUID
func (s *DocumentService) parseAndValidateDocumentID(documentID string) (pgtype.UUID, error) {
	docUUID, err := uuid.Parse(documentID)
	if err != nil {
		return pgtype.UUID{}, status.Errorf(codes.InvalidArgument, "invalid document ID: %v", err)
	}
	return pgtype.UUID{Bytes: docUUID, Valid: true}, nil
}

// resolveTagByPath resolves a tag by its path within a namespace
func (s *DocumentService) resolveTagByPath(
	ctx context.Context,
	namespaceID pgtype.UUID,
	tagPath string,
) (*sqlc.Tag, error) {
	tag, err := s.queries.GetTagByPath(ctx, namespaceID, tagPath)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "tag not found at path %q: %v", tagPath, err)
	}
	return &tag, nil
}

// parseAndValidateAttributesJSON parses JSON and returns the map
func (s *DocumentService) parseAndValidateAttributesJSON(
	attributesJSON string,
) (map[string]interface{}, error) {
	var attributesMap map[string]interface{}
	if err := json.Unmarshal([]byte(attributesJSON), &attributesMap); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid attributes JSON: %v", err)
	}
	return attributesMap, nil
}

// parseExistingMetadata parses existing metadata or returns a default structure
func (s *DocumentService) parseExistingMetadata(
	metadataBytes []byte,
	fallbackExtractedBy string,
) DocumentTagMetadata {
	if len(metadataBytes) > 0 {
		var metadata DocumentTagMetadata
		if err := json.Unmarshal(metadataBytes, &metadata); err == nil {
			return metadata
		}
	}
	// Return default metadata if parsing fails or no metadata exists
	return DocumentTagMetadata{
		Tag: AttributeExtractionInfo{
			Method: ExtractionMethodManual, ExtractedBy: fallbackExtractedBy,
			ExtractedAt: time.Now(), Source: "manual",
		},
	}
}

// updateAttributeExtractionInfo updates extraction info for all attributes in the map
func (s *DocumentService) updateAttributeExtractionInfo(
	metadata *DocumentTagMetadata,
	attributesMap map[string]interface{},
	extractionMethod ExtractionMethod,
	extractedBy string,
) {
	now := time.Now()
	if metadata.Attributes == nil {
		metadata.Attributes = make(map[string]AttributeExtractionInfo)
	}
	for fieldName := range attributesMap {
		metadata.Attributes[fieldName] = AttributeExtractionInfo{
			Method: extractionMethod, ExtractedBy: extractedBy,
			ExtractedAt: now, Source: string(extractionMethod),
		}
	}
}

// marshalMetadata marshals metadata to JSON with error handling
func (s *DocumentService) marshalMetadata(metadata *DocumentTagMetadata) ([]byte, error) {
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal metadata: %v", err)
	}
	return metadataJSON, nil
}

// createAttributeMetadata creates comprehensive extraction metadata for attributes
func (s *DocumentService) createAttributeMetadata(
	attributesMap map[string]interface{},
	extractionMethod ExtractionMethod,
	extractedBy string,
) (*DocumentTagMetadata, error) {
	metadata := &DocumentTagMetadata{
		Tag: AttributeExtractionInfo{
			Method: extractionMethod, ExtractedBy: extractedBy,
			ExtractedAt: time.Now(), Source: string(extractionMethod),
		},
	}
	s.updateAttributeExtractionInfo(metadata, attributesMap, extractionMethod, extractedBy)
	return metadata, nil
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
		DocumentId: docID,
		Namespace:  namespace,
		Filename:   filename,
		MimeType:   mimeType,
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
	// Validate namespace
	ns, err := s.validateNamespace(ctx, namespace)
	if err != nil {
		return err
	}

	// Resolve tag by path
	tag, err := s.resolveTagByPath(ctx, ns.ID, tagPath)
	if err != nil {
		return err
	}

	// Parse and validate document ID
	docPgUUID, err := s.parseAndValidateDocumentID(documentID)
	if err != nil {
		return err
	}

	// Validate attributes if provided
	var attributesData []byte
	var attributesMap map[string]interface{}
	if attributesJSON != nil && *attributesJSON != "" {
		// Parse attributes JSON to validate it's proper JSON
		if err := json.Unmarshal([]byte(*attributesJSON), &attributesMap); err != nil {
			return status.Errorf(codes.InvalidArgument, "invalid attributes JSON: %v", err)
		}

		// Validate attributes against tag's schema using TagService
		if err := s.tagService.ValidateAttributes(ctx, tag.ID, attributesMap); err != nil {
			return status.Errorf(codes.InvalidArgument, "attribute validation failed: %v", err)
		}

		attributesData = []byte(*attributesJSON)
	}

	// Create comprehensive extraction metadata
	metadata, err := s.createAttributeMetadata(attributesMap, extractionMethod, extractedBy)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create metadata: %v", err)
	}

	metadataJSON, err := s.marshalMetadata(metadata)
	if err != nil {
		return err
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

// ListDocumentTags retrieves all tags associated with a document
func (s *DocumentService) ListDocumentTags(
	ctx context.Context,
	namespace string,
	documentID string,
) ([]sqlc.GetDocumentTagsWithAttributesRow, error) {
	// Validate namespace
	ns, err := s.validateNamespace(ctx, namespace)
	if err != nil {
		return nil, err
	}

	// Parse and validate document ID
	docPgUUID, err := s.parseAndValidateDocumentID(documentID)
	if err != nil {
		return nil, err
	}

	// Verify document exists in the specified namespace
	document, err := s.queries.GetDocumentByID(ctx, docPgUUID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "document not found: %v", err)
	}
	if document.NamespaceID != ns.ID {
		return nil, status.Errorf(codes.NotFound, "document not found in namespace %q", namespace)
	}

	// Get document tags with attributes
	tags, err := s.queries.GetDocumentTagsWithAttributes(ctx, docPgUUID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get document tags: %v", err)
	}

	return tags, nil
}

// GetDocumentAttributes retrieves attributes for a document (global) or specific tag
func (s *DocumentService) GetDocumentAttributes(
	ctx context.Context,
	namespace string,
	documentID string,
	tagPath string,
) (*sqlc.GetDocumentTagAttributesRow, error) {
	// Validate namespace
	ns, err := s.validateNamespace(ctx, namespace)
	if err != nil {
		return nil, err
	}

	// Parse and validate document ID
	docPgUUID, err := s.parseAndValidateDocumentID(documentID)
	if err != nil {
		return nil, err
	}

	if tagPath == "" {
		// Handle document global attributes
		document, err := s.queries.GetDocumentByID(ctx, docPgUUID)
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "document not found: %v", err)
		}

		// Convert document attributes to the same structure as tag attributes for consistency
		result := &sqlc.GetDocumentTagAttributesRow{
			Attributes:         document.Attributes,
			AttributesMetadata: document.AttributesMetadata,
		}

		return result, nil
	}

	// Handle tag-specific attributes
	tag, err := s.resolveTagByPath(ctx, ns.ID, tagPath)
	if err != nil {
		return nil, err
	}

	// Get document tag attributes
	attributes, err := s.queries.GetDocumentTagAttributes(ctx, docPgUUID, tag.ID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "document-tag association not found: %v", err)
	}

	return &attributes, nil
}

// UpdateDocumentAttributes updates attributes for a document (global) or specific tag
func (s *DocumentService) UpdateDocumentAttributes(
	ctx context.Context,
	namespace string,
	documentID string,
	tagPath string,
	attributesJSON string,
) error {
	// Validate namespace and parse document ID
	ns, err := s.validateNamespace(ctx, namespace)
	if err != nil {
		return err
	}
	docPgUUID, err := s.parseAndValidateDocumentID(documentID)
	if err != nil {
		return err
	}
	attributesMap, err := s.parseAndValidateAttributesJSON(attributesJSON)
	if err != nil {
		return err
	}

	if tagPath == "" {
		// Handle document global attributes
		metadata, err := s.createAttributeMetadata(
			attributesMap,
			ExtractionMethodManual,
			"api-user",
		)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to create metadata: %v", err)
		}
		metadataJSON, err := s.marshalMetadata(metadata)
		if err != nil {
			return err
		}
		_, err = s.queries.UpdateDocument(
			ctx,
			docPgUUID,
			"",
			"",
			pgtype.Date{},
			"",
			0,
			[]byte(attributesJSON),
			metadataJSON,
		)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to update document attributes: %v", err)
		}
		return nil
	}

	// Handle tag-specific attributes
	tag, err := s.resolveTagByPath(ctx, ns.ID, tagPath)
	if err != nil {
		return err
	}
	if err := s.tagService.ValidateAttributes(ctx, tag.ID, attributesMap); err != nil {
		return status.Errorf(codes.InvalidArgument, "attribute validation failed: %v", err)
	}

	// Get current data and update metadata
	currentData, err := s.queries.GetDocumentTagAttributes(ctx, docPgUUID, tag.ID)
	if err != nil {
		return status.Errorf(codes.NotFound, "document-tag association not found: %v", err)
	}
	metadata := s.parseExistingMetadata(currentData.AttributesMetadata, "api-user")
	s.updateAttributeExtractionInfo(&metadata, attributesMap, ExtractionMethodManual, "api-user")
	updatedMetadataJSON, err := s.marshalMetadata(&metadata)
	if err != nil {
		return err
	}
	err = s.queries.UpdateDocumentTagAttributes(
		ctx,
		docPgUUID,
		tag.ID,
		[]byte(attributesJSON),
		updatedMetadataJSON,
	)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to update tag attributes: %v", err)
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
	// Validate namespace
	ns, err := s.validateNamespace(ctx, namespace)
	if err != nil {
		return err
	}

	// Resolve tag by path
	tag, err := s.resolveTagByPath(ctx, ns.ID, tagPath)
	if err != nil {
		return err
	}

	// Parse and validate document ID
	docPgUUID, err := s.parseAndValidateDocumentID(documentID)
	if err != nil {
		return err
	}

	// Remove the document-tag association
	err = s.queries.RemoveDocumentTag(ctx, docPgUUID, tag.ID)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to remove tag from document: %v", err)
	}

	return nil
}
