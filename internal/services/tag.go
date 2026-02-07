// Package services contains business logic and orchestration
package services

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	eventsv1 "github.com/RynoXLI/Wayfile/gen/go/events/v1"
	"github.com/RynoXLI/Wayfile/internal/db/sqlc"
	"github.com/RynoXLI/Wayfile/internal/events"
)

var (
	// ErrNamespaceNotFound is returned when a namespace doesn't exist
	ErrNamespaceNotFound = errors.New("namespace not found")
	// ErrTagNotFound is returned when a tag doesn't exist
	ErrTagNotFound = errors.New("tag not found")
	// ErrInvalidTagName is returned when a tag name is invalid
	ErrInvalidTagName = errors.New("invalid tag name")
	// ErrInvalidParentName is returned when a parent name is invalid
	ErrInvalidParentName = errors.New("invalid parent name")
	// ErrParentTagNotFound is returned when the parent tag doesn't exist
	ErrParentTagNotFound = errors.New("parent tag not found")
	// ErrInvalidParentReference is returned when the parent reference is invalid
	ErrInvalidParentReference = errors.New("invalid parent reference")
	// ErrInvalidColor is returned when a color is invalid
	ErrInvalidColor = errors.New("invalid color")

	tagNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,98}[a-zA-Z0-9]$|^[a-zA-Z0-9]$`)
	colorRegex   = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

// TagService orchestrates tag operations with schema management and events
type TagService struct {
	queries   *sqlc.Queries
	publisher events.Publisher
}

// NewTagService creates a new tag service
func NewTagService(queries *sqlc.Queries, publisher events.Publisher) *TagService {
	return &TagService{
		queries:   queries,
		publisher: publisher,
	}
}

// generateColorFromName generates a deterministic hex color based on tag name
func generateColorFromName(name string) string {
	// Hash the tag name
	hash := sha256.Sum256([]byte(name))

	// Use first 8 bytes to generate RGB values
	seed := binary.BigEndian.Uint64(hash[:8])

	// Generate RGB values with good saturation and lightness
	// Avoid very dark or very light colors for better visibility
	r := uint8(80 + (seed>>16)%176) // 80-255
	g := uint8(80 + (seed>>8)%176)  // 80-255
	b := uint8(80 + seed%176)       // 80-255

	return fmt.Sprintf("#%02X%02X%02X", r, g, b)
}

// ensureColor returns the provided color or generates one if nil/empty
func (s *TagService) ensureColor(color *string, tagName string) string {
	if color != nil && *color != "" {
		return *color
	}
	return generateColorFromName(tagName)
}

// TagWithSchema represents a tag with its latest schema
type TagWithSchema struct {
	Tag    sqlc.Tag
	Schema *sqlc.AttributeSchema // nil if no schema exists
}

// validateTagInput validates tag name, optional parent name, and color
func (s *TagService) validateTagInput(name string, parentName *string, color *string) error {
	if name == "" {
		return fmt.Errorf("%w: name cannot be empty", ErrInvalidTagName)
	}
	if len(name) > 100 {
		return fmt.Errorf("%w: name cannot exceed 100 characters", ErrInvalidTagName)
	}
	if !tagNameRegex.MatchString(name) {
		return fmt.Errorf(
			"%w: name must contain only alphanumeric characters, hyphens, and underscores, and cannot start or end with hyphen or underscore",
			ErrInvalidTagName,
		)
	}

	if parentName != nil && *parentName != "" {
		if len(*parentName) > 100 {
			return fmt.Errorf("%w: parent name cannot exceed 100 characters", ErrInvalidParentName)
		}
		if !tagNameRegex.MatchString(*parentName) {
			return fmt.Errorf(
				"%w: parent name must contain only alphanumeric characters, hyphens, and underscores, and cannot start or end with hyphen or underscore",
				ErrInvalidParentName,
			)
		}
	}

	if color != nil && *color != "" {
		if !colorRegex.MatchString(*color) {
			return fmt.Errorf(
				"%w: color must be a valid hex color code (e.g., #FF0000)",
				ErrInvalidColor,
			)
		}
	}

	return nil
}

func buildTagPath(parentPath, name string) string {
	if parentPath == "" {
		return "/" + name
	}
	return strings.TrimSuffix(parentPath, "/") + "/" + name
}

func (s *TagService) resolveParentForCreate(
	ctx context.Context,
	namespaceID pgtype.UUID,
	parentName *string,
) (pgtype.UUID, string, error) {
	if parentName == nil || *parentName == "" {
		return pgtype.UUID{}, "", nil
	}

	parent, err := s.queries.GetTagByName(ctx, namespaceID, *parentName)
	if err != nil {
		return pgtype.UUID{}, "", ErrParentTagNotFound
	}

	return parent.ID, parent.Path, nil
}

func (s *TagService) resolveParentForUpdate(
	ctx context.Context,
	namespaceID pgtype.UUID,
	currentTag sqlc.Tag,
	parentName *string,
) (pgtype.UUID, string, error) {
	if parentName == nil {
		if currentTag.ParentID.Valid {
			parent, err := s.queries.GetTagByID(ctx, currentTag.ParentID)
			if err != nil {
				return pgtype.UUID{}, "", ErrParentTagNotFound
			}
			return parent.ID, parent.Path, nil
		}
		return pgtype.UUID{}, "", nil
	}

	if *parentName == "" {
		return pgtype.UUID{}, "", nil
	}

	parent, err := s.queries.GetTagByName(ctx, namespaceID, *parentName)
	if err != nil {
		return pgtype.UUID{}, "", ErrParentTagNotFound
	}

	if parent.ID == currentTag.ID || parent.Path == currentTag.Path ||
		strings.HasPrefix(parent.Path, currentTag.Path+"/") {
		return pgtype.UUID{}, "", ErrInvalidParentReference
	}

	return parent.ID, parent.Path, nil
}

// CreateTag creates a new tag and optionally its schema
func (s *TagService) CreateTag(
	ctx context.Context,
	namespaceName string,
	name string,
	description *string,
	parentName *string,
	color *string,
	jsonSchema *string,
) (*TagWithSchema, error) {
	// Validate input
	if err := s.validateTagInput(name, parentName, color); err != nil {
		return nil, err
	}

	// Ensure color is set (generate if not provided)
	finalColor := s.ensureColor(color, name)

	// Get namespace by name
	namespace, err := s.queries.GetNamespaceByName(ctx, namespaceName)
	if err != nil {
		return nil, ErrNamespaceNotFound
	}

	// Resolve parent and build path
	parentID, parentPath, err := s.resolveParentForCreate(ctx, namespace.ID, parentName)
	if err != nil {
		return nil, err
	}
	path := buildTagPath(parentPath, name)

	// Create the tag
	tag, err := s.queries.CreateTag(
		ctx,
		namespace.ID,
		name,
		description,
		path,
		parentID,
		&finalColor,
	)
	if err != nil {
		return nil, err
	}

	result := &TagWithSchema{Tag: tag}

	// Create schema if provided
	if jsonSchema != nil && *jsonSchema != "" {
		schema, err := s.queries.CreateSchema(ctx, tag.ID, []byte(*jsonSchema))
		if err != nil {
			return nil, err
		}
		result.Schema = &schema

		// Publish schema created event
		_ = s.publisher.TagSchemaChanged(&eventsv1.TagSchemaChangedEvent{
			Namespace:     namespace.Name,
			Tag:           tag.Name,
			OldJsonSchema: "",
			NewJsonSchema: *jsonSchema,
		})
	}

	return result, nil
}

// GetTag retrieves a tag by name within a namespace
func (s *TagService) GetTag(
	ctx context.Context,
	namespaceName string,
	tagName string,
) (*TagWithSchema, error) {
	// Get namespace by name
	namespace, err := s.queries.GetNamespaceByName(ctx, namespaceName)
	if err != nil {
		return nil, ErrNamespaceNotFound
	}

	// Get the tag by name
	tag, err := s.queries.GetTagByName(ctx, namespace.ID, tagName)
	if err != nil {
		return nil, ErrTagNotFound
	}

	result := &TagWithSchema{Tag: tag}

	// Get latest schema if exists
	schema, err := s.queries.GetLatestSchemaByTagID(ctx, tag.ID)
	if err == nil {
		result.Schema = &schema
	}

	return result, nil
}

// GetTagByID retrieves a tag by its UUID within a namespace
func (s *TagService) GetTagByID(
	ctx context.Context,
	namespaceName string,
	tagID pgtype.UUID,
) (*TagWithSchema, error) {
	// Get namespace by name
	namespace, err := s.queries.GetNamespaceByName(ctx, namespaceName)
	if err != nil {
		return nil, ErrNamespaceNotFound
	}

	// Get the tag by ID
	tag, err := s.queries.GetTagByID(ctx, tagID)
	if err != nil {
		return nil, ErrTagNotFound
	}

	// Verify tag belongs to the namespace
	if tag.NamespaceID != namespace.ID {
		return nil, ErrTagNotFound
	}

	result := &TagWithSchema{Tag: tag}

	// Get latest schema if exists
	schema, err := s.queries.GetLatestSchemaByTagID(ctx, tag.ID)
	if err == nil {
		result.Schema = &schema
	}

	return result, nil
}

// ListTags retrieves all tags in a namespace with their schemas
func (s *TagService) ListTags(ctx context.Context, namespaceName string) ([]*TagWithSchema, error) {
	// Get namespace by name
	namespace, err := s.queries.GetNamespaceByName(ctx, namespaceName)
	if err != nil {
		return nil, ErrNamespaceNotFound
	}

	// Get all tags in the namespace
	tags, err := s.queries.GetTagsByNamespace(ctx, namespace.ID)
	if err != nil {
		return nil, err
	}

	// Build results with schemas
	results := make([]*TagWithSchema, len(tags))
	for i, tag := range tags {
		result := &TagWithSchema{Tag: tag}

		// Get latest schema for each tag
		schema, err := s.queries.GetLatestSchemaByTagID(ctx, tag.ID)
		if err == nil {
			result.Schema = &schema
		}

		results[i] = result
	}

	return results, nil
}

// UpdateTag updates a tag and optionally its schema
func (s *TagService) UpdateTag(
	ctx context.Context,
	namespaceName string,
	tagName string,
	newName *string,
	description *string,
	parentName *string,
	color *string,
	jsonSchema *string,
) (*TagWithSchema, error) {
	// Get namespace by name
	namespace, err := s.queries.GetNamespaceByName(ctx, namespaceName)
	if err != nil {
		return nil, ErrNamespaceNotFound
	}

	// Get existing tag
	tag, err := s.queries.GetTagByName(ctx, namespace.ID, tagName)
	if err != nil {
		return nil, ErrTagNotFound
	}

	// Get old schema before update
	var oldSchema *sqlc.AttributeSchema
	schema, err := s.queries.GetLatestSchemaByTagID(ctx, tag.ID)
	if err == nil {
		oldSchema = &schema
	}

	// Determine the new name (use new_name if provided, otherwise keep current)
	updateName := tag.Name
	if newName != nil && *newName != "" {
		updateName = *newName
	}
	if err := s.validateTagInput(updateName, parentName, color); err != nil {
		return nil, err
	}

	// Resolve parent and build path
	parentID, parentPath, err := s.resolveParentForUpdate(ctx, namespace.ID, tag, parentName)
	if err != nil {
		return nil, err
	}
	updatePath := buildTagPath(parentPath, updateName)

	// Ensure color is set (use provided or keep existing)
	var updateColor *string
	if color != nil && *color != "" {
		updateColor = color
	} else if tag.Color != nil {
		updateColor = tag.Color
	} else {
		// Generate color if none exists
		generated := generateColorFromName(updateName)
		updateColor = &generated
	}

	// Update the tag
	tag, err = s.queries.UpdateTag(
		ctx,
		tag.ID,
		updateName,
		description,
		updatePath,
		parentID,
		updateColor,
	)
	if err != nil {
		return nil, err
	}

	result := &TagWithSchema{Tag: tag}

	// Handle schema update
	if jsonSchema != nil && *jsonSchema != "" {
		// Check if schema changed
		var oldSchemaStr string
		if oldSchema != nil {
			oldSchemaStr = string(oldSchema.JsonSchema)
		}
		schemaChanged := oldSchema == nil || oldSchemaStr != *jsonSchema

		if schemaChanged {
			// Create new schema version
			newSchema, err := s.queries.CreateSchema(ctx, tag.ID, []byte(*jsonSchema))
			if err != nil {
				return nil, err
			}
			result.Schema = &newSchema

			// Publish schema changed event
			_ = s.publisher.TagSchemaChanged(&eventsv1.TagSchemaChangedEvent{
				Namespace:     namespace.Name,
				Tag:           tag.Name,
				OldJsonSchema: oldSchemaStr,
				NewJsonSchema: *jsonSchema,
			})
		} else {
			result.Schema = oldSchema
		}
	} else if oldSchema != nil {
		// No new schema provided, keep the old one
		result.Schema = oldSchema
	}

	return result, nil
}

// DeleteTag removes a tag
func (s *TagService) DeleteTag(ctx context.Context, namespaceName string, tagName string) error {
	// Get namespace by name
	namespace, err := s.queries.GetNamespaceByName(ctx, namespaceName)
	if err != nil {
		return ErrNamespaceNotFound
	}

	// Get the tag to delete
	tag, err := s.queries.GetTagByName(ctx, namespace.ID, tagName)
	if err != nil {
		return ErrTagNotFound
	}

	// Delete the tag
	return s.queries.DeleteTag(ctx, tag.ID)
}
