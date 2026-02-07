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
		parentName *string
		color      *string
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
			name:       "valid tag with parent name",
			tagName:    "2024",
			parentName: stringPtr("invoices"),
			color:      nil,
			wantErr:    false,
		},
		{
			name:       "valid parent name with hyphen",
			tagName:    "child",
			parentName: stringPtr("my-parent"),
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
			name:       "parent name with spaces",
			tagName:    "test",
			parentName: stringPtr("my parent"),
			color:      nil,
			wantErr:    true,
			errString:  "parent name must contain only alphanumeric",
		},
		{
			name:       "parent name with special characters",
			tagName:    "test",
			parentName: stringPtr("parent@"),
			color:      nil,
			wantErr:    true,
			errString:  "parent name must contain only alphanumeric",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.validateTagInput(tt.tagName, tt.parentName, tt.color)
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
