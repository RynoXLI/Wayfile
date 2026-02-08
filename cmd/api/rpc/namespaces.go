// Package rpc implements Connect RPC service handlers
package rpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	namespacesv1 "github.com/RynoXLI/Wayfile/gen/go/namespaces/v1"
	"github.com/RynoXLI/Wayfile/internal/services"
)

// NamespaceServiceServer implements the Connect RPC NamespaceService
type NamespaceServiceServer struct {
	service *services.NamespaceService
}

// NewNamespaceServiceServer creates a new Connect RPC service for namespaces
func NewNamespaceServiceServer(service *services.NamespaceService) *NamespaceServiceServer {
	return &NamespaceServiceServer{
		service: service,
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
	namespace, err := s.service.CreateNamespace(ctx, req.Name)
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

// ListNamespaces retrieves all namespaces via Connect RPC
func (s *NamespaceServiceServer) ListNamespaces(
	ctx context.Context,
	_ *namespacesv1.ListNamespacesRequest,
) (*namespacesv1.ListNamespacesResponse, error) {
	// Get all namespaces
	namespaces, err := s.service.ListNamespaces(ctx)
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

	return &namespacesv1.ListNamespacesResponse{
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
	namespace, err := s.service.GetNamespace(ctx, req.Name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("namespace not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
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
	err := s.service.DeleteNamespace(ctx, req.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return &namespacesv1.DeleteNamespaceResponse{}, nil
}
