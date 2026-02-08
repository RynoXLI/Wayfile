// Package services contains business logic and orchestration
package services

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/santhosh-tekuri/jsonschema/v5"

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
	// ErrTagAlreadyExists is returned when a tag with the same path already exists
	ErrTagAlreadyExists = errors.New("tag with this path already exists")
	// ErrInvalidJSONSchema is returned when a JSON schema is malformed
	ErrInvalidJSONSchema = errors.New("invalid JSON schema")
	// ErrNestedTypesNotAllowed is returned when a JSON schema contains nested objects or arrays
	ErrNestedTypesNotAllowed = errors.New(
		"nested types not allowed in schema, only primitive types are supported",
	)
	// ErrAttributeValidationFailed is returned when attributes don't match the tag's schema
	ErrAttributeValidationFailed = errors.New("attribute validation failed")

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

// metaSchemaForPrimitivesOnly defines a JSON schema that validates other schemas
// to ensure they only contain primitive types in properties
const metaSchemaForPrimitivesOnly = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["type"],
  "properties": {
    "type": {
      "const": "object"
    },
    "properties": {
      "type": "object",
      "additionalProperties": {
        "type": "object",
        "required": ["type"],
        "anyOf": [
          {
            "properties": {
              "type": {"const": "string"},
              "minLength": {"type": "integer", "minimum": 0},
              "maxLength": {"type": "integer", "minimum": 0},
              "pattern": {"type": "string"},
              "format": {"type": "string", "enum": ["date", "time", "date-time", "email", "uuid", "uri", "hostname", "ipv4", "ipv6"]},
              "enum": {"type": "array", "items": {"type": "string"}}
            },
            "additionalProperties": true
          },
          {
            "properties": {
              "type": {"const": "number"},
              "minimum": {"type": "number"},
              "maximum": {"type": "number"},
              "enum": {"type": "array", "items": {"type": "number"}}
            },
            "additionalProperties": true
          },
          {
            "properties": {
              "type": {"const": "integer"},
              "minimum": {"type": "integer"},
              "maximum": {"type": "integer"},
              "enum": {"type": "array", "items": {"type": "integer"}}
            },
            "additionalProperties": true
          },
          {
            "properties": {
              "type": {"const": "boolean"}
            },
            "additionalProperties": true
          }
        ]
      }
    },
    "required": {
      "type": "array",
      "items": {"type": "string"}
    }
  },
  "additionalProperties": true
}`

// validateSchemaPrimitivesOnly validates that a JSON schema only contains primitive types
func validateSchemaPrimitivesOnly(schemaJSON string) error {
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020

	// Parse the input schema to validate it's well-formed JSON
	var inputSchema interface{}
	if err := json.Unmarshal([]byte(schemaJSON), &inputSchema); err != nil {
		return fmt.Errorf("failed to parse schema JSON: %w", err)
	}

	// First, validate it's a proper JSON schema by compiling it
	err := compiler.AddResource("input-schema", strings.NewReader(schemaJSON))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidJSONSchema, err)
	}

	_, err = compiler.Compile("input-schema")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidJSONSchema, err)
	}

	// Now validate against our meta-schema that enforces primitive-only properties
	err = compiler.AddResource(
		"primitives-meta-schema",
		strings.NewReader(metaSchemaForPrimitivesOnly),
	)
	if err != nil {
		return fmt.Errorf("internal error: failed to add meta-schema: %w", err)
	}

	metaSchema, err := compiler.Compile("primitives-meta-schema")
	if err != nil {
		return fmt.Errorf("internal error: failed to compile meta-schema: %w", err)
	}

	// Validate the input schema against our meta-schema
	if err := metaSchema.Validate(inputSchema); err != nil {
		return fmt.Errorf("%w: %v", ErrNestedTypesNotAllowed, err)
	}

	return nil
}

// isPrimitiveType checks if a JSON Schema type is a primitive type
func isPrimitiveType(schemaType string) bool {
	primitives := map[string]bool{
		"string":  true,
		"number":  true,
		"integer": true,
		"boolean": true,
	}
	return primitives[schemaType]
}

// ValidateAttributes validates attribute data against a tag's schema
func (s *TagService) ValidateAttributes(
	ctx context.Context,
	tagID pgtype.UUID,
	attributes map[string]interface{},
) error {
	// Get the tag's latest schema
	schema, err := s.queries.GetLatestSchemaByTagID(ctx, tagID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No schema exists, so no validation needed
			return nil
		}
		return fmt.Errorf("failed to get schema for tag: %w", err)
	}

	// Compile the schema for validation
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020

	err = compiler.AddResource("tag-schema", strings.NewReader(string(schema.JsonSchema)))
	if err != nil {
		return fmt.Errorf("failed to add schema resource: %w", err)
	}

	compiledSchema, err := compiler.Compile("tag-schema")
	if err != nil {
		return fmt.Errorf("failed to compile schema: %w", err)
	}

	// Validate the attributes against the schema
	if err := compiledSchema.Validate(attributes); err != nil {
		return fmt.Errorf("%w: %v", ErrAttributeValidationFailed, err)
	}

	return nil
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

// validateTagInput validates tag name, optional parent path, color, and JSON schema
func (s *TagService) validateTagInput(
	name string,
	parentPath *string,
	color *string,
	jsonSchema *string,
) error {
	// Validate name
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

	// Validate parent path
	if parentPath != nil && *parentPath != "" {
		if len(*parentPath) > 255 {
			return fmt.Errorf("%w: parent path cannot exceed 255 characters", ErrInvalidParentName)
		}
		if !strings.HasPrefix(*parentPath, "/") {
			return fmt.Errorf("%w: parent path must start with /", ErrInvalidParentName)
		}
	}

	// Validate color
	if color != nil && *color != "" {
		if !colorRegex.MatchString(*color) {
			return fmt.Errorf(
				"%w: color must be a valid hex color code (e.g., #FF0000)",
				ErrInvalidColor,
			)
		}
	}

	// Validate JSON schema if provided
	if jsonSchema != nil && *jsonSchema != "" {
		if !json.Valid([]byte(*jsonSchema)) {
			return fmt.Errorf("%w: provided JSON schema is not valid JSON", ErrInvalidJSONSchema)
		}
		// Validate that schema only contains primitive types
		if err := validateSchemaPrimitivesOnly(*jsonSchema); err != nil {
			return err
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
	parentPath *string,
) (pgtype.UUID, string, error) {
	if parentPath == nil || *parentPath == "" {
		return pgtype.UUID{}, "", nil
	}

	parent, err := s.queries.GetTagByPath(ctx, namespaceID, *parentPath)
	if err != nil {
		return pgtype.UUID{}, "", ErrParentTagNotFound
	}

	return parent.ID, parent.Path, nil
}

func (s *TagService) resolveParentForUpdate(
	ctx context.Context,
	namespaceID pgtype.UUID,
	currentTag sqlc.Tag,
	parentPath *string,
) (pgtype.UUID, string, error) {
	if parentPath == nil {
		if currentTag.ParentID.Valid {
			parent, err := s.queries.GetTagByID(ctx, currentTag.ParentID)
			if err != nil {
				return pgtype.UUID{}, "", ErrParentTagNotFound
			}
			return parent.ID, parent.Path, nil
		}
		return pgtype.UUID{}, "", nil
	}

	if *parentPath == "" {
		return pgtype.UUID{}, "", nil
	}

	parent, err := s.queries.GetTagByPath(ctx, namespaceID, *parentPath)
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
	parentPath *string,
	color *string,
	jsonSchema *string,
) (*TagWithSchema, error) {
	// Validate input
	if err := s.validateTagInput(name, parentPath, color, jsonSchema); err != nil {
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
	parentID, resolvedParentPath, err := s.resolveParentForCreate(ctx, namespace.ID, parentPath)
	if err != nil {
		return nil, err
	}
	path := buildTagPath(resolvedParentPath, name)

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
		// Check for unique constraint violation on path
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// 23505 is unique_violation
			return nil, ErrTagAlreadyExists
		}
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
		_ = s.publisher.SchemaChanged(&eventsv1.SchemaChangedEvent{
			Namespace:     namespace.Name,
			TagPath:       tag.Path,
			OldJsonSchema: "",
			NewJsonSchema: *jsonSchema,
		})
	}

	return result, nil
}

// GetTagByPath retrieves a tag by its hierarchical path within a namespace
func (s *TagService) GetTagByPath(
	ctx context.Context,
	namespaceName string,
	tagPath string,
) (*TagWithSchema, error) {
	// Get namespace by name
	namespace, err := s.queries.GetNamespaceByName(ctx, namespaceName)
	if err != nil {
		return nil, ErrNamespaceNotFound
	}

	// Get the tag by path
	tag, err := s.queries.GetTagByPath(ctx, namespace.ID, tagPath)
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
	tagPath string,
	newName *string,
	description *string,
	parentPath *string,
	color *string,
	jsonSchema *string,
) (*TagWithSchema, error) {
	// Get namespace by name
	namespace, err := s.queries.GetNamespaceByName(ctx, namespaceName)
	if err != nil {
		return nil, ErrNamespaceNotFound
	}

	// Get existing tag
	tag, err := s.queries.GetTagByPath(ctx, namespace.ID, tagPath)
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
	if err := s.validateTagInput(updateName, parentPath, color, jsonSchema); err != nil {
		return nil, err
	}

	// Resolve parent and build path
	parentID, resolvedParentPath, err := s.resolveParentForUpdate(
		ctx,
		namespace.ID,
		tag,
		parentPath,
	)
	if err != nil {
		return nil, err
	}
	updatePath := buildTagPath(resolvedParentPath, updateName)

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
		// Check for unique constraint violation on path
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// 23505 is unique_violation
			return nil, ErrTagAlreadyExists
		}
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
			_ = s.publisher.SchemaChanged(&eventsv1.SchemaChangedEvent{
				Namespace:     namespace.Name,
				TagPath:       tag.Path,
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
func (s *TagService) DeleteTag(ctx context.Context, namespaceName string, tagPath string) error {
	// Get namespace by name
	namespace, err := s.queries.GetNamespaceByName(ctx, namespaceName)
	if err != nil {
		return ErrNamespaceNotFound
	}

	// Get the tag to delete
	tag, err := s.queries.GetTagByPath(ctx, namespace.ID, tagPath)
	if err != nil {
		return ErrTagNotFound
	}

	// Delete the tag
	return s.queries.DeleteTag(ctx, tag.ID)
}
