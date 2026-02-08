// Package rpc implements Connect RPC service handlers
package rpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	documentsv1 "github.com/RynoXLI/Wayfile/gen/go/documents/v1"
	"github.com/RynoXLI/Wayfile/internal/services"
	"github.com/RynoXLI/Wayfile/internal/storage"
)

// DocumentsServiceServer implements the Connect RPC DocumentService
type DocumentsServiceServer struct {
	documentService *services.DocumentService
}

// NewDocumentsServiceServer creates a new Connect RPC service
func NewDocumentsServiceServer(documentService *services.DocumentService) *DocumentsServiceServer {
	return &DocumentsServiceServer{
		documentService: documentService,
	}
}

// DeleteDocument handles document deletion via Connect RPC
func (s *DocumentsServiceServer) DeleteDocument(
	ctx context.Context,
	req *documentsv1.DeleteDocumentRequest,
) (*documentsv1.DeleteDocumentResponse, error) {
	// Validate namespace
	if req.Namespace == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("namespace is required"),
		)
	}

	// Validate UUID
	if _, err := uuid.Parse(req.DocumentId); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Delete the document
	err := s.documentService.DeleteDocument(ctx, req.Namespace, req.DocumentId)
	if err != nil {
		if err == storage.ErrNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return &documentsv1.DeleteDocumentResponse{}, nil
}

// UpdateDocument handles document updates via Connect RPC (stub for now)
func (s *DocumentsServiceServer) UpdateDocument(
	_ context.Context,
	_ *documentsv1.UpdateDocumentRequest,
) (*documentsv1.UpdateDocumentResponse, error) {
	// TODO: Implement update logic
	return nil, connect.NewError(
		connect.CodeUnimplemented,
		errors.New("update document not yet implemented"),
	)
}

// AddTagToDocument handles adding a tag to a document via Connect RPC
func (s *DocumentsServiceServer) AddTagToDocument(
	ctx context.Context,
	req *documentsv1.AddTagToDocumentRequest,
) (*documentsv1.AddTagToDocumentResponse, error) {
	// Validate required fields
	if req.Namespace == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("namespace is required"),
		)
	}
	if req.DocumentId == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("document_id is required"),
		)
	}
	if req.TagPath == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("tag_path is required"),
		)
	}

	// Validate document ID
	if _, err := uuid.Parse(req.DocumentId); err != nil {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("invalid document_id format"),
		)
	}

	// Add the tag to the document
	err := s.documentService.AddTagToDocument(
		ctx,
		req.Namespace,
		req.DocumentId,
		req.TagPath,
		req.Attributes,
		services.ExtractionMethodManual,
		"api-user", // Could be enhanced to get actual user info from context
	)
	if err != nil {
		if errors.Is(err, services.ErrDocumentNotInNamespace) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		if errors.Is(err, services.ErrTagNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return &documentsv1.AddTagToDocumentResponse{}, nil
}

// RemoveTagFromDocument handles removing a tag from a document via Connect RPC
func (s *DocumentsServiceServer) RemoveTagFromDocument(
	ctx context.Context,
	req *documentsv1.RemoveTagFromDocumentRequest,
) (*documentsv1.RemoveTagFromDocumentResponse, error) {
	// Validate required fields
	if req.Namespace == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("namespace is required"),
		)
	}
	if req.DocumentId == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("document_id is required"),
		)
	}
	if req.TagPath == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("tag_path is required"),
		)
	}

	// Validate document ID
	if _, err := uuid.Parse(req.DocumentId); err != nil {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("invalid document_id format"),
		)
	}

	// Remove the tag from the document
	err := s.documentService.RemoveTagFromDocument(
		ctx,
		req.Namespace,
		req.DocumentId,
		req.TagPath,
	)
	if err != nil {
		if errors.Is(err, services.ErrDocumentNotInNamespace) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		if errors.Is(err, services.ErrTagNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return &documentsv1.RemoveTagFromDocumentResponse{}, nil
}

// ListDocumentTags handles listing all tags on a document via Connect RPC
func (s *DocumentsServiceServer) ListDocumentTags(
	ctx context.Context,
	req *documentsv1.ListDocumentTagsRequest,
) (*documentsv1.ListDocumentTagsResponse, error) {
	// Validate required fields
	if req.Namespace == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("namespace is required"),
		)
	}
	if req.DocumentId == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("document_id is required"),
		)
	}

	// Get the document tags
	tags, err := s.documentService.ListDocumentTags(
		ctx,
		req.Namespace,
		req.DocumentId,
	)
	if err != nil {
		return nil, err
	}

	// Convert to protobuf response
	documentTags := make([]*documentsv1.DocumentTag, len(tags))
	for i, tag := range tags {
		documentTag := &documentsv1.DocumentTag{
			Name:    tag.Name,
			TagPath: tag.Path,
		}

		// Add attributes if present
		if len(tag.Attributes) > 0 {
			attributesStr := string(tag.Attributes)
			documentTag.Attributes = &attributesStr
		}

		// Add extraction metadata if present
		if len(tag.AttributesMetadata) > 0 {
			metadataStr := string(tag.AttributesMetadata)
			documentTag.Metadata = &metadataStr
		}

		// Set updated timestamp
		documentTag.UpdatedAt = timestamppb.New(tag.ModifiedAt.Time)

		documentTags[i] = documentTag
	}

	return &documentsv1.ListDocumentTagsResponse{
		Tags: documentTags,
	}, nil
}

// GetDocumentAttributes handles getting attributes for a document (global) or specific tag via Connect RPC
func (s *DocumentsServiceServer) GetDocumentAttributes(
	ctx context.Context,
	req *documentsv1.GetDocumentAttributesRequest,
) (*documentsv1.GetDocumentAttributesResponse, error) {
	// Validate required fields
	if req.Namespace == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("namespace is required"),
		)
	}
	if req.DocumentId == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("document_id is required"),
		)
	}

	// tag_path is optional - empty means document global attributes
	tagPath := ""
	if req.TagPath != nil {
		tagPath = *req.TagPath
	}

	// Get the document attributes
	attributes, err := s.documentService.GetDocumentAttributes(
		ctx,
		req.Namespace,
		req.DocumentId,
		tagPath,
	)
	if err != nil {
		if errors.Is(err, services.ErrDocumentNotInNamespace) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		if errors.Is(err, services.ErrTagNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	response := &documentsv1.GetDocumentAttributesResponse{}

	// Add attributes if present
	if len(attributes.Attributes) > 0 {
		attributesStr := string(attributes.Attributes)
		response.Attributes = &attributesStr
	}

	// Add extraction metadata if present
	if len(attributes.AttributesMetadata) > 0 {
		metadataStr := string(attributes.AttributesMetadata)
		response.Metadata = &metadataStr
	}

	return response, nil
}

// UpdateDocumentAttributes handles updating attributes for a document (global) or specific tag via Connect RPC
func (s *DocumentsServiceServer) UpdateDocumentAttributes(
	ctx context.Context,
	req *documentsv1.UpdateDocumentAttributesRequest,
) (*documentsv1.UpdateDocumentAttributesResponse, error) {
	// Validate required fields
	if req.Namespace == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("namespace is required"),
		)
	}
	if req.DocumentId == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("document_id is required"),
		)
	}
	if req.Attributes == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("attributes is required"),
		)
	}

	// tag_path is optional - empty means document global attributes
	tagPath := ""
	if req.TagPath != nil {
		tagPath = *req.TagPath
	}

	// Update the attributes
	err := s.documentService.UpdateDocumentAttributes(
		ctx,
		req.Namespace,
		req.DocumentId,
		tagPath,
		req.Attributes,
	)
	if err != nil {
		if errors.Is(err, services.ErrDocumentNotInNamespace) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		if errors.Is(err, services.ErrTagNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, err
	}

	return &documentsv1.UpdateDocumentAttributesResponse{}, nil
}
