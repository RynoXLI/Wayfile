package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateTagInput(t *testing.T) {
	s := &TagService{}

	tests := []struct {
		name       string
		tagName    string
		parentPath *string
		color      *string
		jsonSchema *string
		wantErr    bool
		errString  string
	}{
		{
			name:    "valid tag with no parent",
			tagName: "invoices",
			color:   stringPtr("#FF0000"),
			wantErr: false,
		},
		{
			name:       "valid tag with parent path",
			tagName:    "2024",
			parentPath: stringPtr("/invoices"),
			color:      nil,
			wantErr:    false,
		},
		{
			name:       "valid parent path with hyphen",
			tagName:    "child",
			parentPath: stringPtr("/my-parent"),
			color:      nil,
			wantErr:    false,
		},
		{
			name:    "valid single character name",
			tagName: "a",
			color:   nil,
			wantErr: false,
		},
		{
			name:      "empty name",
			tagName:   "",
			color:     nil,
			wantErr:   true,
			errString: "name cannot be empty",
		},
		{
			name:      "name too long",
			tagName:   "a" + string(make([]byte, 100)),
			color:     nil,
			wantErr:   true,
			errString: "name cannot exceed 100 characters",
		},
		{
			name:      "name starts with hyphen",
			tagName:   "-invalid",
			color:     nil,
			wantErr:   true,
			errString: "name must contain only alphanumeric",
		},
		{
			name:      "name ends with underscore",
			tagName:   "invalid_",
			color:     nil,
			wantErr:   true,
			errString: "name must contain only alphanumeric",
		},
		{
			name:      "name with spaces",
			tagName:   "my tag",
			color:     nil,
			wantErr:   true,
			errString: "name must contain only alphanumeric",
		},
		{
			name:      "name with special characters",
			tagName:   "tag@name",
			color:     nil,
			wantErr:   true,
			errString: "name must contain only alphanumeric",
		},
		{
			name:       "parent path without leading slash",
			tagName:    "test",
			parentPath: stringPtr("my-parent"),
			color:      nil,
			wantErr:    true,
			errString:  "parent path must start with /",
		},
		{
			name:       "parent path too long",
			tagName:    "test",
			parentPath: stringPtr("/" + string(make([]byte, 256))), // 257 chars
			color:      nil,
			wantErr:    true,
			errString:  "parent path cannot exceed 255 characters",
		},
		{
			name:      "invalid color format",
			tagName:   "test",
			color:     stringPtr("FF0000"),
			wantErr:   true,
			errString: "color must be a valid hex color code",
		},
		{
			name:      "invalid color length",
			tagName:   "test",
			color:     stringPtr("#FFF"),
			wantErr:   true,
			errString: "color must be a valid hex color code",
		},
		{
			name:      "invalid color characters",
			tagName:   "test",
			color:     stringPtr("#GGGGGG"),
			wantErr:   true,
			errString: "color must be a valid hex color code",
		},
		{
			name:    "valid color uppercase",
			tagName: "test",
			color:   stringPtr("#ABCDEF"),
			wantErr: false,
		},
		{
			name:    "valid color lowercase",
			tagName: "test",
			color:   stringPtr("#abc123"),
			wantErr: false,
		},
		{
			name:    "empty color string is valid",
			tagName: "test",
			color:   stringPtr(""),
			wantErr: false,
		},
		{
			name:       "valid nested parent path",
			tagName:    "leaf",
			parentPath: stringPtr("/root/branch/node"),
			color:      nil,
			wantErr:    false,
		},
		{
			name:       "valid deeply nested parent path",
			tagName:    "deep",
			parentPath: stringPtr("/a/very/deep/nested/hierarchy/path"),
			color:      nil,
			wantErr:    false,
		},
		{
			name:       "parent path with trailing slash",
			tagName:    "child",
			parentPath: stringPtr("/parent/"),
			color:      nil,
			wantErr:    false,
		},
		{
			name:       "parent path with multiple consecutive slashes",
			tagName:    "child",
			parentPath: stringPtr("/parent//subparent"),
			color:      nil,
			wantErr:    false,
		},
		{
			name:    "valid JSON schema with primitives only",
			tagName: "test",
			jsonSchema: stringPtr(
				`{"type": "object", "properties": {"name": {"type": "string"}, "age": {"type": "integer"}, "active": {"type": "boolean"}}}`,
			),
			wantErr: false,
		},
		{
			name:    "invalid JSON schema - contains object type",
			tagName: "test",
			jsonSchema: stringPtr(
				`{"type": "object", "properties": {"address": {"type": "object", "properties": {"street": {"type": "string"}}}}}`,
			),
			wantErr:   true,
			errString: "nested types not allowed",
		},
		{
			name:    "invalid JSON schema - contains array type",
			tagName: "test",
			jsonSchema: stringPtr(
				`{"type": "object", "properties": {"tags": {"type": "array", "items": {"type": "string"}}}}`,
			),
			wantErr:   true,
			errString: "nested types not allowed",
		},
		{
			name:    "valid JSON schema with all primitive types",
			tagName: "test",
			jsonSchema: stringPtr(
				`{"type": "object", "properties": {"str": {"type": "string"}, "num": {"type": "number"}, "int": {"type": "integer"}, "bool": {"type": "boolean"}}}`,
			),
			wantErr: false,
		},
		{
			name:       "invalid JSON schema - malformed JSON",
			tagName:    "test",
			jsonSchema: stringPtr(`{"type": "object", "properties": {`),
			wantErr:    true,
			errString:  "invalid JSON schema",
		},
		{
			name:       "invalid JSON schema - not JSON at all",
			tagName:    "test",
			jsonSchema: stringPtr(`not json at all`),
			wantErr:    true,
			errString:  "invalid JSON schema",
		},
		{
			name:       "empty JSON schema string is valid",
			tagName:    "test",
			jsonSchema: stringPtr(""),
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.validateTagInput(tt.tagName, tt.parentPath, tt.color, tt.jsonSchema)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errString != "" {
					assert.Contains(t, err.Error(), tt.errString)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGenerateColorFromName(t *testing.T) {
	tests := []struct {
		name     string
		tagName  string
		expected string
	}{
		{
			name:     "same name generates same color",
			tagName:  "invoices",
			expected: generateColorFromName("invoices"),
		},
		{
			name:     "different names generate different colors",
			tagName:  "receipts",
			expected: generateColorFromName("receipts"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			color := generateColorFromName(tt.tagName)

			// Verify format
			assert.Regexp(t, `^#[0-9A-F]{6}$`, color)

			// Verify deterministic
			assert.Equal(t, tt.expected, color)
			assert.Equal(t, color, generateColorFromName(tt.tagName))
		})
	}

	// Verify different names produce different colors
	color1 := generateColorFromName("tag1")
	color2 := generateColorFromName("tag2")
	assert.NotEqual(t, color1, color2)
}

func TestEnsureColor(t *testing.T) {
	s := &TagService{}

	tests := []struct {
		name     string
		color    *string
		tagName  string
		expected string
	}{
		{
			name:     "provided color is used",
			color:    stringPtr("#FF0000"),
			tagName:  "test",
			expected: "#FF0000",
		},
		{
			name:     "nil color generates from name",
			color:    nil,
			tagName:  "invoices",
			expected: generateColorFromName("invoices"),
		},
		{
			name:     "empty color generates from name",
			color:    stringPtr(""),
			tagName:  "receipts",
			expected: generateColorFromName("receipts"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.ensureColor(tt.color, tt.tagName)
			assert.Equal(t, tt.expected, result)

			// Verify format
			assert.Regexp(t, `^#[0-9A-F]{6}$`, result)
		})
	}
}

func stringPtr(s string) *string {
	return &s
}

func TestValidateSchemaPrimitivesOnly(t *testing.T) {
	tests := []struct {
		name      string
		schema    string
		wantErr   bool
		errString string
	}{
		{
			name:    "valid schema with primitive types",
			schema:  `{"type": "object", "properties": {"name": {"type": "string"}, "age": {"type": "integer"}, "active": {"type": "boolean"}}}`,
			wantErr: false,
		},
		{
			name:      "invalid schema with object type",
			schema:    `{"type": "object", "properties": {"address": {"type": "object"}}}`,
			wantErr:   true,
			errString: "nested types not allowed",
		},
		{
			name:      "invalid schema with array type",
			schema:    `{"type": "object", "properties": {"tags": {"type": "array"}}}`,
			wantErr:   true,
			errString: "nested types not allowed",
		},
		{
			name:    "valid schema with all primitives",
			schema:  `{"type": "object", "properties": {"str": {"type": "string"}, "num": {"type": "number"}, "int": {"type": "integer"}, "bool": {"type": "boolean"}}}`,
			wantErr: false,
		},
		{
			name:      "invalid schema with nested object",
			schema:    `{"type": "object", "properties": {"user": {"type": "object", "properties": {"name": {"type": "string"}}}}}`,
			wantErr:   true,
			errString: "nested types not allowed",
		},
		{
			name:    "valid schema with string validation rules",
			schema:  `{"type": "object", "properties": {"name": {"type": "string", "minLength": 1, "maxLength": 100}, "email": {"type": "string", "pattern": "^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}$"}}}`,
			wantErr: false,
		},
		{
			name:    "valid schema with number validation rules",
			schema:  `{"type": "object", "properties": {"price": {"type": "number", "minimum": 0, "maximum": 1000}, "rating": {"type": "integer", "minimum": 1, "maximum": 5}}}`,
			wantErr: false,
		},
		{
			name:    "valid schema with enum validation",
			schema:  `{"type": "object", "properties": {"status": {"type": "string", "enum": ["active", "inactive"]}, "priority": {"type": "integer", "enum": [1, 2, 3]}}}`,
			wantErr: false,
		},
		{
			name:    "valid schema with required fields",
			schema:  `{"type": "object", "properties": {"name": {"type": "string"}, "age": {"type": "integer"}}, "required": ["name"]}`,
			wantErr: false,
		},
		{
			name:    "valid schema with format validation",
			schema:  `{"type": "object", "properties": {"email": {"type": "string", "format": "email"}, "created_date": {"type": "string", "format": "date"}, "doc_id": {"type": "string", "format": "uuid"}}}`,
			wantErr: false,
		},
		{
			name:      "invalid JSON",
			schema:    `{"invalid": json}`,
			wantErr:   true,
			errString: "failed to parse schema JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSchemaPrimitivesOnly(tt.schema)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errString != "" {
					assert.Contains(t, err.Error(), tt.errString)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestIsPrimitiveType(t *testing.T) {
	tests := []struct {
		schemaType string
		expected   bool
	}{
		{"string", true},
		{"number", true},
		{"integer", true},
		{"boolean", true},
		{"null", false},
		{"object", false},
		{"array", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.schemaType, func(t *testing.T) {
			result := isPrimitiveType(tt.schemaType)
			assert.Equal(t, tt.expected, result)
		})
	}
}
