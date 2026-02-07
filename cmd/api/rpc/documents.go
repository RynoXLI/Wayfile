// Package rpc implements Connect RPC service handlers
package rpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"

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
	)
	if err != nil {
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
		return nil, err
	}

	return &documentsv1.RemoveTagFromDocumentResponse{}, nil
}
