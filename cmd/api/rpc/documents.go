// Package rpc implements Connect RPC service handlers
package rpc

import (
	"context"

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
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
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
) (*documentsv1.Document, error) {
	// TODO: Implement update logic
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}
