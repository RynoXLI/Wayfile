// Package services contains business logic and orchestration
package services

import (
	"context"

	"github.com/RynoXLI/Wayfile/internal/db/sqlc"
)

// NamespaceService orchestrates namespace operations
type NamespaceService struct {
	queries *sqlc.Queries
}

// NewNamespaceService creates a new namespace service
func NewNamespaceService(queries *sqlc.Queries) *NamespaceService {
	return &NamespaceService{
		queries: queries,
	}
}

// CreateNamespace creates a new namespace
func (s *NamespaceService) CreateNamespace(
	ctx context.Context,
	name string,
) (sqlc.Namespace, error) {
	return s.queries.CreateNamespace(ctx, name)
}

// ListNamespaces retrieves all namespaces
func (s *NamespaceService) ListNamespaces(ctx context.Context) ([]sqlc.Namespace, error) {
	return s.queries.GetNamespaces(ctx)
}

// GetNamespace retrieves a specific namespace by name
func (s *NamespaceService) GetNamespace(ctx context.Context, name string) (sqlc.Namespace, error) {
	return s.queries.GetNamespaceByName(ctx, name)
}

// DeleteNamespace removes a namespace
func (s *NamespaceService) DeleteNamespace(ctx context.Context, name string) error {
	return s.queries.DeleteNamespace(ctx, name)
}
