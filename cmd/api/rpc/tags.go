// Package rpc implements Connect RPC service handlers
package rpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	tagsv1 "github.com/RynoXLI/Wayfile/gen/go/tags/v1"
	"github.com/RynoXLI/Wayfile/internal/db/sqlc"
	"github.com/RynoXLI/Wayfile/internal/services"
)

// TagServiceServer implements the Connect RPC TagService
type TagServiceServer struct {
	service *services.TagService
}

// NewTagServiceServer creates a new Connect RPC service for tags
func NewTagServiceServer(service *services.TagService) *TagServiceServer {
	return &TagServiceServer{
		service: service,
	}
}

// CreateTag handles tag creation via Connect RPC
func (s *TagServiceServer) CreateTag(
	ctx context.Context,
	req *tagsv1.CreateTagRequest,
) (*tagsv1.CreateTagResponse, error) {
	// Validate tag name
	if req.Name == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("tag name is required"),
		)
	}

	// Validate namespace
	if req.Namespace == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("namespace is required"),
		)
	}

	// Create the tag via service
	result, err := s.service.CreateTag(
		ctx,
		req.Namespace,
		req.Name,
		req.Description,
		req.ParentName,
		req.Color,
		req.JsonSchema,
	)
	if err != nil {
		if errors.Is(err, services.ErrNamespaceNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		if errors.Is(err, services.ErrParentTagNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		if errors.Is(err, services.ErrInvalidParentReference) ||
			errors.Is(err, services.ErrInvalidTagName) ||
			errors.Is(err, services.ErrInvalidParentName) ||
			errors.Is(err, services.ErrInvalidColor) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return &tagsv1.CreateTagResponse{
		Tag: s.convertTagToProto(ctx, result.Tag, result.Schema, req.Namespace),
	}, nil
}

// GetTag retrieves a specific tag by name within a namespace via Connect RPC
func (s *TagServiceServer) GetTag(
	ctx context.Context,
	req *tagsv1.GetTagRequest,
) (*tagsv1.GetTagResponse, error) {
	// Validate namespace
	if req.Namespace == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("namespace is required"),
		)
	}

	// Validate tag name
	if req.Name == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("tag name is required"),
		)
	}

	// Get the tag via service
	result, err := s.service.GetTag(ctx, req.Namespace, req.Name)
	if err != nil {
		if errors.Is(err, services.ErrNamespaceNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("namespace not found"))
		}
		if errors.Is(err, services.ErrTagNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return &tagsv1.GetTagResponse{
		Tag: s.convertTagToProto(ctx, result.Tag, result.Schema, req.Namespace),
	}, nil
}

// ListTags retrieves all tags in a namespace via Connect RPC
func (s *TagServiceServer) ListTags(
	ctx context.Context,
	req *tagsv1.ListTagsRequest,
) (*tagsv1.ListTagsResponse, error) {
	// Validate namespace
	if req.Namespace == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("namespace is required"),
		)
	}

	// Get all tags via service
	results, err := s.service.ListTags(ctx, req.Namespace)
	if err != nil {
		if errors.Is(err, services.ErrNamespaceNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("namespace not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert to protobuf format
	pbTags := make([]*tagsv1.Tag, len(results))
	for i, result := range results {
		pbTags[i] = s.convertTagToProto(ctx, result.Tag, result.Schema, req.Namespace)
	}

	return &tagsv1.ListTagsResponse{
		Tags: pbTags,
	}, nil
}

// UpdateTag handles tag updates via Connect RPC
func (s *TagServiceServer) UpdateTag(
	ctx context.Context,
	req *tagsv1.UpdateTagRequest,
) (*tagsv1.UpdateTagResponse, error) {
	// Validate namespace
	if req.Namespace == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("namespace is required"),
		)
	}

	// Validate tag name
	if req.Name == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("tag name is required"),
		)
	}

	// Update the tag via service
	result, err := s.service.UpdateTag(
		ctx,
		req.Namespace,
		req.Name,
		req.NewName,
		req.Description,
		req.ParentName,
		req.Color,
		req.JsonSchema,
	)
	if err != nil {
		if errors.Is(err, services.ErrNamespaceNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("namespace not found"))
		}
		if errors.Is(err, services.ErrTagNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("tag not found"))
		}
		if errors.Is(err, services.ErrParentTagNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		if errors.Is(err, services.ErrInvalidParentReference) ||
			errors.Is(err, services.ErrInvalidTagName) ||
			errors.Is(err, services.ErrInvalidParentName) ||
			errors.Is(err, services.ErrInvalidColor) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return &tagsv1.UpdateTagResponse{
		Tag: s.convertTagToProto(ctx, result.Tag, result.Schema, req.Namespace),
	}, nil
}

// DeleteTag handles tag deletion via Connect RPC
func (s *TagServiceServer) DeleteTag(
	ctx context.Context,
	req *tagsv1.DeleteTagRequest,
) (*tagsv1.DeleteTagResponse, error) {
	// Validate namespace
	if req.Namespace == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("namespace is required"),
		)
	}

	// Validate tag name
	if req.Name == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("tag name is required"),
		)
	}

	// Delete the tag via service
	err := s.service.DeleteTag(ctx, req.Namespace, req.Name)
	if err != nil {
		if errors.Is(err, services.ErrNamespaceNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("namespace not found"))
		}
		if errors.Is(err, services.ErrTagNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("tag not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return &tagsv1.DeleteTagResponse{}, nil
}

// convertTagToProto converts a sqlc Tag to a protobuf Tag
func (s *TagServiceServer) convertTagToProto(
	_ context.Context,
	tag sqlc.Tag,
	schema *sqlc.AttributeSchema,
	_ string,
) *tagsv1.Tag {
	pbTag := &tagsv1.Tag{
		Name:       tag.Name,
		Path:       tag.Path,
		CreatedAt:  timestamppb.New(tag.CreatedAt.Time),
		ModifiedAt: timestamppb.New(tag.ModifiedAt.Time),
	}

	if tag.Description != nil {
		pbTag.Description = tag.Description
	}

	// Parent relationship is implicit in the path, no need to expose parent_id

	if tag.Color != nil {
		pbTag.Color = tag.Color
	}

	if schema != nil {
		schemaStr := string(schema.JsonSchema)
		pbTag.JsonSchema = &schemaStr
	}

	return pbTag
}
