// Package rpc implements Connect RPC service handlers
package rpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	namespacesv1 "github.com/RynoXLI/Wayfile/gen/go/namespaces/v1"
	"github.com/RynoXLI/Wayfile/internal/db/sqlc"
)

// NamespaceServiceServer implements the Connect RPC NamespaceService
type NamespaceServiceServer struct {
	queries *sqlc.Queries
}

// NewNamespaceServiceServer creates a new Connect RPC service for namespaces
func NewNamespaceServiceServer(queries *sqlc.Queries) *NamespaceServiceServer {
	return &NamespaceServiceServer{
		queries: queries,
	}
}

// CreateNamespace handles namespace creation via Connect RPC
func (s *NamespaceServiceServer) CreateNamespace(
	ctx context.Context,
	req *namespacesv1.CreateNamespaceRequest,
) (*namespacesv1.CreateNamespaceResponse, error) {
	// Validate namespace name
	if req.Name == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("namespace name is required"),
		)
	}

	// Create the namespace
	namespace, err := s.queries.CreateNamespace(ctx, req.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return &namespacesv1.CreateNamespaceResponse{
		Namespace: &namespacesv1.Namespace{
			Id:         namespace.ID.String(),
			Name:       namespace.Name,
			CreatedAt:  timestamppb.New(namespace.CreatedAt.Time),
			ModifiedAt: timestamppb.New(namespace.ModifiedAt.Time),
		},
	}, nil
}

// GetNamespaces retrieves all namespaces via Connect RPC
func (s *NamespaceServiceServer) GetNamespaces(
	ctx context.Context,
	_ *namespacesv1.GetNamespacesRequest,
) (*namespacesv1.GetNamespacesResponse, error) {
	// Get all namespaces
	namespaces, err := s.queries.GetNamespaces(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert to protobuf format
	pbNamespaces := make([]*namespacesv1.Namespace, len(namespaces))
	for i, ns := range namespaces {
		pbNamespaces[i] = &namespacesv1.Namespace{
			Id:         ns.ID.String(),
			Name:       ns.Name,
			CreatedAt:  timestamppb.New(ns.CreatedAt.Time),
			ModifiedAt: timestamppb.New(ns.ModifiedAt.Time),
		}
	}

	return &namespacesv1.GetNamespacesResponse{
		Namespaces: pbNamespaces,
	}, nil
}

// GetNamespace retrieves a specific namespace by name via Connect RPC
func (s *NamespaceServiceServer) GetNamespace(
	ctx context.Context,
	req *namespacesv1.GetNamespaceRequest,
) (*namespacesv1.GetNamespaceResponse, error) {
	// Validate namespace name
	if req.Name == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("namespace name is required"),
		)
	}

	// Get the namespace
	namespace, err := s.queries.GetNamespaceByName(ctx, req.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return &namespacesv1.GetNamespaceResponse{
		Namespace: &namespacesv1.Namespace{
			Id:         namespace.ID.String(),
			Name:       namespace.Name,
			CreatedAt:  timestamppb.New(namespace.CreatedAt.Time),
			ModifiedAt: timestamppb.New(namespace.ModifiedAt.Time),
		},
	}, nil
}

// DeleteNamespace handles namespace deletion via Connect RPC
func (s *NamespaceServiceServer) DeleteNamespace(
	ctx context.Context,
	req *namespacesv1.DeleteNamespaceRequest,
) (*namespacesv1.DeleteNamespaceResponse, error) {
	// Validate namespace name
	if req.Name == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("namespace name is required"),
		)
	}

	// Delete the namespace
	err := s.queries.DeleteNamespace(ctx, req.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return &namespacesv1.DeleteNamespaceResponse{}, nil
}
