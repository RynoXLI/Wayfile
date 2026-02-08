//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	documentsv1 "github.com/RynoXLI/Wayfile/gen/go/documents/v1"
	namespacesv1 "github.com/RynoXLI/Wayfile/gen/go/namespaces/v1"
	tagsv1 "github.com/RynoXLI/Wayfile/gen/go/tags/v1"
)

// TestTagCRUD tests the tag CRUD operations via Connect RPC
func TestTagCRUD(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	ctx := context.Background()

	// Create a namespace first since tags belong to namespaces
	nsResp, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "test-namespace",
	})
	require.NoError(t, err)
	require.NotNil(t, nsResp)

	// === Step 1: Create a tag ===
	createResp, err := ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "test-namespace",
		Name:        "financial",
		Description: stringPtr("Financial documents"),
		Color:       stringPtr("#FF5733"),
	})
	require.NoError(t, err)
	require.NotNil(t, createResp)
	require.NotNil(t, createResp.Tag)
	require.Equal(t, "financial", createResp.Tag.Name)
	require.Equal(t, "/financial", createResp.Tag.Path)
	require.Equal(t, "#FF5733", *createResp.Tag.Color)
	require.NotNil(t, createResp.Tag.CreatedAt)
	require.NotNil(t, createResp.Tag.ModifiedAt)

	// === Step 2: Create another tag ===
	createResp2, err := ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "test-namespace",
		Name:        "reports",
		Description: stringPtr("Report documents"),
		Color:       stringPtr("#3366FF"),
	})
	require.NoError(t, err)
	require.NotNil(t, createResp2)
	require.NotNil(t, createResp2.Tag)
	require.Equal(t, "reports", createResp2.Tag.Name)

	// === Step 3: Create a tag with JSON schema ===
	jsonSchema := `{
		"type": "object",
		"properties": {
			"year": {"type": "integer"},
			"quarter": {"type": "string", "enum": ["Q1", "Q2", "Q3", "Q4"]}
		},
		"required": ["year"]
	}`
	createResp3, err := ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:  "test-namespace",
		Name:       "quarterly",
		JsonSchema: stringPtr(jsonSchema),
	})
	require.NoError(t, err)
	require.NotNil(t, createResp3)
	require.NotNil(t, createResp3.Tag)
	require.Equal(t, "quarterly", createResp3.Tag.Name)
	require.NotNil(t, createResp3.Tag.JsonSchema)
	AssertJSONEqual(t, jsonSchema, *createResp3.Tag.JsonSchema, "JSON schema should match")

	// === Step 4: Get all tags in namespace ===
	listResp, err := ta.TagClient.ListTags(ctx, &tagsv1.ListTagsRequest{
		Namespace: "test-namespace",
	})
	require.NoError(t, err)
	require.NotNil(t, listResp)
	require.GreaterOrEqual(t, len(listResp.Tags), 3, "Should have at least 3 tags")

	// Check that our tags are in the list
	foundFinancial := false
	foundReports := false
	foundQuarterly := false
	for _, tag := range listResp.Tags {
		switch tag.Name {
		case "financial":
			foundFinancial = true
		case "reports":
			foundReports = true
		case "quarterly":
			foundQuarterly = true
		}
	}
	require.True(t, foundFinancial, "financial tag should be in the list")
	require.True(t, foundReports, "reports tag should be in the list")
	require.True(t, foundQuarterly, "quarterly tag should be in the list")

	// === Step 5: Get specific tag by name ===
	getResp, err := ta.TagClient.GetTag(ctx, &tagsv1.GetTagRequest{
		Namespace: "test-namespace",
		Name:      "financial",
	})
	require.NoError(t, err)
	require.NotNil(t, getResp)
	require.NotNil(t, getResp.Tag)
	require.Equal(t, "financial", getResp.Tag.Name)
	require.NotNil(t, getResp.Tag.Description)
	require.Equal(t, "Financial documents", *getResp.Tag.Description)
	require.Equal(t, "/financial", getResp.Tag.Path)
	require.Equal(t, "#FF5733", *getResp.Tag.Color)

	// === Step 6: Try to get non-existent tag ===
	_, err = ta.TagClient.GetTag(ctx, &tagsv1.GetTagRequest{
		Namespace: "test-namespace",
		Name:      "nonexistent",
	})
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	require.Equal(
		t,
		connect.CodeNotFound,
		connectErr.Code(),
		"Non-existent tag should return NotFound",
	)

	// === Step 7: Update tag description and color ===
	updateResp, err := ta.TagClient.UpdateTag(ctx, &tagsv1.UpdateTagRequest{
		Namespace:   "test-namespace",
		Name:        "financial",
		Description: stringPtr("Updated financial documents"),
		Color:       stringPtr("#00FF00"),
	})
	require.NoError(t, err)
	require.NotNil(t, updateResp)
	require.NotNil(t, updateResp.Tag)
	require.Equal(t, "financial", updateResp.Tag.Name)
	require.Equal(t, "Updated financial documents", *updateResp.Tag.Description)
	require.Equal(t, "#00FF00", *updateResp.Tag.Color)
	require.Equal(t, "/financial", updateResp.Tag.Path) // Path should remain unchanged

	// === Step 8: Verify update persisted ===
	getResp2, err := ta.TagClient.GetTag(ctx, &tagsv1.GetTagRequest{
		Namespace: "test-namespace",
		Name:      "financial",
	})
	require.NoError(t, err)
	require.NotNil(t, getResp2)
	require.NotNil(t, getResp2.Tag)
	require.Equal(t, "Updated financial documents", *getResp2.Tag.Description)
	require.Equal(t, "#00FF00", *getResp2.Tag.Color)

	// === Step 9: Rename a tag ===
	updateResp2, err := ta.TagClient.UpdateTag(ctx, &tagsv1.UpdateTagRequest{
		Namespace: "test-namespace",
		Name:      "reports",
		NewName:   stringPtr("annual-reports"),
	})
	require.NoError(t, err)
	require.NotNil(t, updateResp2)
	require.NotNil(t, updateResp2.Tag)
	require.Equal(t, "annual-reports", updateResp2.Tag.Name)

	// === Step 10: Verify old name is gone and new name exists ===
	_, err = ta.TagClient.GetTag(ctx, &tagsv1.GetTagRequest{
		Namespace: "test-namespace",
		Name:      "reports",
	})
	require.Error(t, err)
	require.ErrorAs(t, err, &connectErr)
	require.Equal(t, connect.CodeNotFound, connectErr.Code())

	getResp3, err := ta.TagClient.GetTag(ctx, &tagsv1.GetTagRequest{
		Namespace: "test-namespace",
		Name:      "annual-reports",
	})
	require.NoError(t, err)
	require.NotNil(t, getResp3)
	require.Equal(t, "annual-reports", getResp3.Tag.Name)
	require.Equal(t, "/annual-reports", getResp3.Tag.Path)

	// === Step 11: Delete a tag ===
	deleteResp, err := ta.TagClient.DeleteTag(ctx, &tagsv1.DeleteTagRequest{
		Namespace: "test-namespace",
		Name:      "financial",
	})
	require.NoError(t, err)
	require.NotNil(t, deleteResp)

	// === Step 12: Verify tag was deleted ===
	_, err = ta.TagClient.GetTag(ctx, &tagsv1.GetTagRequest{
		Namespace: "test-namespace",
		Name:      "financial",
	})
	require.Error(t, err)
	require.ErrorAs(t, err, &connectErr)
	require.Equal(t, connect.CodeNotFound, connectErr.Code(), "Deleted tag should not be found")

	// === Step 13: Verify other tags still exist ===
	getResp4, err := ta.TagClient.GetTag(ctx, &tagsv1.GetTagRequest{
		Namespace: "test-namespace",
		Name:      "annual-reports",
	})
	require.NoError(t, err)
	require.NotNil(t, getResp4)
	require.Equal(t, "annual-reports", getResp4.Tag.Name)

	// === Step 14: Test validation - empty namespace name on create ===
	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace: "",
		Name:      "test",
	})
	require.Error(t, err)
	require.ErrorAs(t, err, &connectErr)
	require.Equal(
		t,
		connect.CodeInvalidArgument,
		connectErr.Code(),
		"Empty namespace should return InvalidArgument",
	)

	// === Step 15: Test validation - empty tag name on create ===
	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace: "test-namespace",
		Name:      "",
	})
	require.Error(t, err)
	require.ErrorAs(t, err, &connectErr)
	require.Equal(
		t,
		connect.CodeInvalidArgument,
		connectErr.Code(),
		"Empty tag name should return InvalidArgument",
	)
}

// TestTagHierarchy tests parent-child tag relationships
func TestTagHierarchy(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	ctx := context.Background()

	// Create a namespace first
	_, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "hierarchy-test",
	})
	require.NoError(t, err)

	// === Step 1: Create a parent tag ===
	parentResp, err := ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "hierarchy-test",
		Name:        "documents",
		Description: stringPtr("All documents"),
		Color:       stringPtr("#0000FF"),
	})
	require.NoError(t, err)
	require.NotNil(t, parentResp)
	require.NotNil(t, parentResp.Tag)
	require.Equal(t, "documents", parentResp.Tag.Name)
	require.Equal(t, "/documents", parentResp.Tag.Path)

	// === Step 2: Create a child tag with parent_id ===
	childResp, err := ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "hierarchy-test",
		Name:        "invoices",
		Description: stringPtr("Invoice documents"),
		ParentPath:  stringPtr("/documents"),
		Color:       stringPtr("#00FF00"),
	})
	require.NoError(t, err)
	require.NotNil(t, childResp)
	require.NotNil(t, childResp.Tag)
	require.Equal(t, "invoices", childResp.Tag.Name)
	require.Equal(t, "/documents/invoices", childResp.Tag.Path)

	// === Step 3: Create another child tag under the same parent ===
	child2Resp, err := ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "hierarchy-test",
		Name:        "receipts",
		Description: stringPtr("Receipt documents"),
		ParentPath:  stringPtr("/documents"),
		Color:       stringPtr("#FF0000"),
	})
	require.NoError(t, err)
	require.NotNil(t, child2Resp)
	require.NotNil(t, child2Resp.Tag)
	require.Equal(t, "receipts", child2Resp.Tag.Name)
	require.Equal(t, "/documents/receipts", child2Resp.Tag.Path)

	// === Step 4: Create a grandchild tag (3 levels deep) with dash in name ===
	grandchildResp, err := ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "hierarchy-test",
		Name:        "2024-invoices",
		Description: stringPtr("2024 invoices"),
		ParentPath:  stringPtr("/documents/invoices"),
		Color:       stringPtr("#FFFF00"),
	})
	require.NoError(t, err)
	require.NotNil(t, grandchildResp)
	require.NotNil(t, grandchildResp.Tag)
	require.Equal(t, "2024-invoices", grandchildResp.Tag.Name)
	require.Equal(t, "/documents/invoices/2024-invoices", grandchildResp.Tag.Path)

	// === Step 5: List all tags and verify hierarchy ===
	listResp, err := ta.TagClient.ListTags(ctx, &tagsv1.ListTagsRequest{
		Namespace: "hierarchy-test",
	})
	require.NoError(t, err)
	require.NotNil(t, listResp)
	require.GreaterOrEqual(t, len(listResp.Tags), 4, "Should have at least 4 tags")

	// Verify all tags are present
	tagNames := make(map[string]bool)
	for _, tag := range listResp.Tags {
		tagNames[tag.Name] = true
	}
	require.True(t, tagNames["documents"], "Parent tag should exist")
	require.True(t, tagNames["invoices"], "Child tag should exist")
	require.True(t, tagNames["receipts"], "Child tag should exist")
	require.True(t, tagNames["2024-invoices"], "Grandchild tag with dash should exist")

	// === Step 6: Get child tag and verify parent relationship ===
	getChildResp, err := ta.TagClient.GetTag(ctx, &tagsv1.GetTagRequest{
		Namespace: "hierarchy-test",
		Name:      "invoices",
	})
	require.NoError(t, err)
	require.NotNil(t, getChildResp)
	require.NotNil(t, getChildResp.Tag)
	require.Equal(t, "/documents/invoices", getChildResp.Tag.Path)

	// === Step 7: Update path to change hierarchy ===
	updateResp, err := ta.TagClient.UpdateTag(ctx, &tagsv1.UpdateTagRequest{
		Namespace:  "hierarchy-test",
		Name:       "receipts",
		ParentPath: stringPtr("/documents/invoices"), // Move receipts under invoices
	})
	require.NoError(t, err)
	require.NotNil(t, updateResp)
	require.NotNil(t, updateResp.Tag)
	require.Equal(t, "receipts", updateResp.Tag.Name)
	require.Equal(t, "/documents/invoices/receipts", updateResp.Tag.Path)

	// === Step 8: Verify updated hierarchy via path ===
	getUpdatedResp, err := ta.TagClient.GetTag(ctx, &tagsv1.GetTagRequest{
		Namespace: "hierarchy-test",
		Name:      "receipts",
	})
	require.NoError(t, err)
	require.NotNil(t, getUpdatedResp)
	require.NotNil(t, getUpdatedResp.Tag)
	require.Equal(t, "/documents/invoices/receipts", getUpdatedResp.Tag.Path)
}

func TestTagAutoGeneratedColor(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	ctx := context.Background()

	// === Step 1: Create namespace ===
	_, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "color-test",
	})
	require.NoError(t, err)

	// === Step 2: Create tag WITHOUT providing a color ===
	tagResp1, err := ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "color-test",
		Name:        "no-color-tag",
		Description: stringPtr("Tag without explicit color"),
		// Color is intentionally nil
	})
	require.NoError(t, err)
	require.NotNil(t, tagResp1)
	require.NotNil(t, tagResp1.Tag)
	require.NotNil(t, tagResp1.Tag.Color, "Color should be auto-generated")
	require.Regexp(t, `^#[0-9A-F]{6}$`, *tagResp1.Tag.Color, "Color should be valid hex format")

	// === Step 3: Create another tag with same name (different namespace) - should get same color ===
	_, err = ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "color-test-2",
	})
	require.NoError(t, err)

	tagResp2, err := ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "color-test-2",
		Name:        "no-color-tag",
		Description: stringPtr("Same name tag in different namespace"),
	})
	require.NoError(t, err)
	require.NotNil(t, tagResp2.Tag.Color)
	require.Equal(
		t,
		*tagResp1.Tag.Color,
		*tagResp2.Tag.Color,
		"Same tag name should generate same color",
	)

	// === Step 4: Create tag with different name - should get different color ===
	tagResp3, err := ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace: "color-test",
		Name:      "different-tag",
	})
	require.NoError(t, err)
	require.NotNil(t, tagResp3.Tag.Color)
	require.NotEqual(
		t,
		*tagResp1.Tag.Color,
		*tagResp3.Tag.Color,
		"Different tag names should generate different colors",
	)

	// === Step 5: Create tag WITH explicit color - should use provided color ===
	explicitColor := "#FF00FF"
	tagResp4, err := ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace: "color-test",
		Name:      "explicit-color-tag",
		Color:     &explicitColor,
	})
	require.NoError(t, err)
	require.NotNil(t, tagResp4.Tag.Color)
	require.Equal(t, explicitColor, *tagResp4.Tag.Color, "Explicit color should be preserved")
}

func TestTagDeletionCascade(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	ctx := context.Background()

	// === Step 1: Create namespace ===
	_, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "cascade-test",
	})
	require.NoError(t, err)

	// === Step 2: Create parent tag ===
	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace: "cascade-test",
		Name:      "parent",
	})
	require.NoError(t, err)

	// === Step 3: Create child tags ===
	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:  "cascade-test",
		Name:       "child1",
		ParentPath: stringPtr("/parent"),
	})
	require.NoError(t, err)

	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:  "cascade-test",
		Name:       "child2",
		ParentPath: stringPtr("/parent"),
	})
	require.NoError(t, err)

	// === Step 4: Create grandchild tag ===
	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:  "cascade-test",
		Name:       "grandchild",
		ParentPath: stringPtr("/parent/child1"),
	})
	require.NoError(t, err)

	// === Step 5: Verify all tags exist ===
	listResp, err := ta.TagClient.ListTags(ctx, &tagsv1.ListTagsRequest{
		Namespace: "cascade-test",
	})
	require.NoError(t, err)
	require.Len(t, listResp.Tags, 4, "Should have 4 tags")

	// === Step 6: Delete parent tag ===
	_, err = ta.TagClient.DeleteTag(ctx, &tagsv1.DeleteTagRequest{
		Namespace: "cascade-test",
		Name:      "parent",
	})
	require.NoError(t, err)

	// === Step 7: Verify parent is deleted ===
	_, err = ta.TagClient.GetTag(ctx, &tagsv1.GetTagRequest{
		Namespace: "cascade-test",
		Name:      "parent",
	})
	require.Error(t, err, "Parent tag should be deleted")

	// === Step 8: Verify children are deleted (CASCADE) ===
	_, err = ta.TagClient.GetTag(ctx, &tagsv1.GetTagRequest{
		Namespace: "cascade-test",
		Name:      "child1",
	})
	require.Error(t, err, "Child1 should be cascade deleted")

	_, err = ta.TagClient.GetTag(ctx, &tagsv1.GetTagRequest{
		Namespace: "cascade-test",
		Name:      "child2",
	})
	require.Error(t, err, "Child2 should be cascade deleted")

	_, err = ta.TagClient.GetTag(ctx, &tagsv1.GetTagRequest{
		Namespace: "cascade-test",
		Name:      "grandchild",
	})
	require.Error(t, err, "Grandchild should be cascade deleted")

	// === Step 9: List should be empty ===
	listResp, err = ta.TagClient.ListTags(ctx, &tagsv1.ListTagsRequest{
		Namespace: "cascade-test",
	})
	require.NoError(t, err)
	require.Empty(t, listResp.Tags, "All tags should be deleted")
}

func TestTagCyclePrevention(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	ctx := context.Background()

	// === Step 1: Create namespace ===
	_, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "cycle-test",
	})
	require.NoError(t, err)

	// === Step 2: Create parent tag ===
	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace: "cycle-test",
		Name:      "parent",
	})
	require.NoError(t, err)

	// === Step 3: Create child tag ===
	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:  "cycle-test",
		Name:       "child",
		ParentPath: stringPtr("/parent"),
	})
	require.NoError(t, err)

	// === Step 4: Prevent self-parenting ===
	_, err = ta.TagClient.UpdateTag(ctx, &tagsv1.UpdateTagRequest{
		Namespace:  "cycle-test",
		Name:       "parent",
		ParentPath: stringPtr("/parent"),
	})
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	require.Equal(t, connect.CodeInvalidArgument, connectErr.Code())

	// === Step 5: Prevent parent -> descendant cycle ===
	_, err = ta.TagClient.UpdateTag(ctx, &tagsv1.UpdateTagRequest{
		Namespace:  "cycle-test",
		Name:       "parent",
		ParentPath: stringPtr("/parent/child"),
	})
	require.Error(t, err)
	require.ErrorAs(t, err, &connectErr)
	require.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
}

func TestDocumentTagging(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	ctx := context.Background()

	// === Step 1: Create namespace ===
	_, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "doc-tag-test",
	})
	require.NoError(t, err)

	// === Step 2: Create tags ===
	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace: "doc-tag-test",
		Name:      "invoice",
	})
	require.NoError(t, err)

	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace: "doc-tag-test",
		Name:      "urgent",
	})
	require.NoError(t, err)

	// === Step 3: Upload a document ===
	fileContent := []byte("test content")
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="file"; filename="test-document.txt"`}
	h["Content-Type"] = []string{"text/plain; charset=utf-8"}
	part, err := writer.CreatePart(h)
	require.NoError(t, err)

	_, err = part.Write(fileContent)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ns/doc-tag-test/documents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp := httptest.NewRecorder()

	ta.Router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusCreated, resp.Code)

	var uploadResponse DocumentResponse
	err = json.NewDecoder(resp.Body).Decode(&uploadResponse)
	require.NoError(t, err)
	require.NotEmpty(t, uploadResponse.ID)

	// === Step 4: Apply tags to document via RPC ===
	_, err = ta.ConnectClient.AddTagToDocument(ctx, &documentsv1.AddTagToDocumentRequest{
		Namespace:  "doc-tag-test",
		DocumentId: uploadResponse.ID,
		TagPath:    "/invoice",
	})
	require.NoError(t, err)

	_, err = ta.ConnectClient.AddTagToDocument(ctx, &documentsv1.AddTagToDocumentRequest{
		Namespace:  "doc-tag-test",
		DocumentId: uploadResponse.ID,
		TagPath:    "/urgent",
	})
	require.NoError(t, err)

	// Note: Tag verification would be done through a ListDocumentTags API in a complete implementation
	// For now, we trust the AddTagToDocument operations succeeded

	// === Step 5: Verify document still accessible ===
	getReq := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/v1/ns/doc-tag-test/documents/%s", uploadResponse.ID),
		nil,
	)
	getResp := httptest.NewRecorder()
	ta.Router.ServeHTTP(getResp, getReq)
	require.Equal(t, http.StatusOK, getResp.Code)
}

func TestTagDuplicatePaths(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	ctx := context.Background()

	// Create namespace
	_, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "duplicate-test",
	})
	require.NoError(t, err)

	// === Step 1: Create parent tag ===
	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "duplicate-test",
		Name:        "parent",
		Description: stringPtr("Parent tag"),
	})
	require.NoError(t, err)

	// === Step 2: Create first child tag ===
	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "duplicate-test",
		Name:        "child",
		Description: stringPtr("First child tag"),
		ParentPath:  stringPtr("/parent"),
	})
	require.NoError(t, err)

	// === Step 3: Try to create duplicate child tag (same name, same parent) ===
	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "duplicate-test",
		Name:        "child", // Same name as existing child
		Description: stringPtr("Duplicate child tag"),
		ParentPath:  stringPtr("/parent"), // Same parent path
	})
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	// Should fail due to unique constraint on (namespace_id, path)
	require.Equal(t, connect.CodeAlreadyExists, connectErr.Code())

	// === Step 4: Create another parent ===
	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "duplicate-test",
		Name:        "parent2",
		Description: stringPtr("Second parent tag"),
	})
	require.NoError(t, err)

	// === Step 5: Create child under second parent (same name, different parent - should work) ===
	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "duplicate-test",
		Name:        "child", // Same name but different parent path
		Description: stringPtr("Child under parent2"),
		ParentPath:  stringPtr("/parent2"),
	})
	require.NoError(t, err) // This should work - different paths: /parent/child vs /parent2/child

	// === Step 6: Try to update tag to create duplicate path ===
	_, err = ta.TagClient.UpdateTag(ctx, &tagsv1.UpdateTagRequest{
		Namespace: "duplicate-test",
		Name:      "child", // This is the child under /parent2
		ParentPath: stringPtr(
			"/parent",
		), // Try to move it under /parent (would create duplicate /parent/child)
	})
	require.Error(t, err)
	require.ErrorAs(t, err, &connectErr)
	// Should fail due to unique constraint violation
	require.Equal(t, connect.CodeAlreadyExists, connectErr.Code())
}

func TestTagDuplicateAtRoot(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	ctx := context.Background()

	// Create namespace
	_, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "root-duplicate-test",
	})
	require.NoError(t, err)

	// === Step 1: Create root tag ===
	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "root-duplicate-test",
		Name:        "documents",
		Description: stringPtr("Documents tag"),
	})
	require.NoError(t, err)

	// === Step 2: Try to create duplicate root tag (same name, no parent) ===
	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "root-duplicate-test",
		Name:        "documents", // Same name
		Description: stringPtr("Duplicate documents tag"),
		// No ParentPath specified - should create /documents again
	})
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	// Should fail due to unique constraint on path (/documents already exists)
	require.Equal(t, connect.CodeAlreadyExists, connectErr.Code())
}

func TestTagSchemaCreation(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	ctx := context.Background()

	// Create namespace
	_, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "schema-test",
	})
	require.NoError(t, err)

	// === Test 1: Create tag with schema ===
	invoiceSchema := `{
		"type": "object",
		"properties": {
			"amount": {"type": "number", "minimum": 0},
			"currency": {"type": "string", "enum": ["USD", "EUR", "GBP"]},
			"date": {"type": "string", "format": "date"},
			"vendor": {"type": "string", "minLength": 1}
		},
		"required": ["amount", "currency", "date"],
		"additionalProperties": false
	}`

	tagResp, err := ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "schema-test",
		Name:        "invoice",
		Description: stringPtr("Invoice tag with schema"),
		JsonSchema:  stringPtr(invoiceSchema),
	})
	require.NoError(t, err)
	require.NotNil(t, tagResp.Tag)
	require.Equal(t, "invoice", tagResp.Tag.Name)
	require.NotNil(t, tagResp.Tag.JsonSchema)
	AssertJSONEqual(t, invoiceSchema, *tagResp.Tag.JsonSchema, "Schema should match")

	// === Test 2: Create hierarchical tag with schema ===
	expenseSchema := `{
		"type": "object",
		"properties": {
			"category": {"type": "string", "enum": ["travel", "meals", "supplies"]},
			"amount": {"type": "number", "minimum": 0},
			"approved": {"type": "boolean", "default": false}
		},
		"required": ["category", "amount"]
	}`

	childTagResp, err := ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "schema-test",
		Name:        "expense",
		Description: stringPtr("Expense tag with schema"),
		ParentPath:  stringPtr("/invoice"),
		JsonSchema:  stringPtr(expenseSchema),
	})
	require.NoError(t, err)
	require.NotNil(t, childTagResp.Tag)
	require.Equal(t, "expense", childTagResp.Tag.Name)
	require.Equal(t, "/invoice/expense", childTagResp.Tag.Path)
	require.NotNil(t, childTagResp.Tag.JsonSchema)
	AssertJSONEqual(t, expenseSchema, *childTagResp.Tag.JsonSchema, "Child tag schema should match")

	// === Test 3: Create tag without schema ===
	simpleTagResp, err := ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "schema-test",
		Name:        "simple",
		Description: stringPtr("Tag without schema"),
	})
	require.NoError(t, err)
	require.NotNil(t, simpleTagResp.Tag)
	require.Equal(t, "simple", simpleTagResp.Tag.Name)
	require.Nil(t, simpleTagResp.Tag.JsonSchema)

	// === Test 4: Update tag to add schema ===
	updateSchema := `{
		"type": "object",
		"properties": {
			"priority": {"type": "string", "enum": ["low", "medium", "high"]},
			"created_at": {"type": "string", "format": "date-time"}
		}
	}`

	updatedTagResp, err := ta.TagClient.UpdateTag(ctx, &tagsv1.UpdateTagRequest{
		Namespace:  "schema-test",
		Name:       "simple",
		JsonSchema: stringPtr(updateSchema),
	})
	require.NoError(t, err)
	require.NotNil(t, updatedTagResp.Tag)
	require.NotNil(t, updatedTagResp.Tag.JsonSchema)
	AssertJSONEqual(t, updateSchema, *updatedTagResp.Tag.JsonSchema, "Updated schema should match")

	// === Test 5: Update tag to modify schema ===
	modifiedSchema := `{
		"type": "object",
		"properties": {
			"priority": {"type": "string", "enum": ["urgent", "normal", "low"]},
			"created_at": {"type": "string", "format": "date-time"},
			"tag_count": {"type": "integer", "minimum": 0}
		},
		"required": ["priority"]
	}`

	modifiedTagResp, err := ta.TagClient.UpdateTag(ctx, &tagsv1.UpdateTagRequest{
		Namespace:  "schema-test",
		Name:       "simple",
		JsonSchema: stringPtr(modifiedSchema),
	})
	require.NoError(t, err)
	require.NotNil(t, modifiedTagResp.Tag)
	require.NotNil(t, modifiedTagResp.Tag.JsonSchema)
	AssertJSONEqual(
		t,
		modifiedSchema,
		*modifiedTagResp.Tag.JsonSchema,
		"Modified schema should match",
	)

	// === Test 6: List tags to verify schemas are preserved ===
	listResp, err := ta.TagClient.ListTags(ctx, &tagsv1.ListTagsRequest{
		Namespace: "schema-test",
	})
	require.NoError(t, err)
	require.Len(t, listResp.Tags, 3)

	// Find each tag and verify schemas
	var invoiceTag, expenseTag, simpleTag *tagsv1.Tag
	for _, tag := range listResp.Tags {
		switch tag.Name {
		case "invoice":
			invoiceTag = tag
		case "expense":
			expenseTag = tag
		case "simple":
			simpleTag = tag
		}
	}

	require.NotNil(t, invoiceTag)
	require.NotNil(t, invoiceTag.JsonSchema)
	AssertJSONEqual(t, invoiceSchema, *invoiceTag.JsonSchema, "Listed invoice schema should match")

	require.NotNil(t, expenseTag)
	require.NotNil(t, expenseTag.JsonSchema)
	AssertJSONEqual(t, expenseSchema, *expenseTag.JsonSchema, "Listed expense schema should match")

	require.NotNil(t, simpleTag)
	require.NotNil(t, simpleTag.JsonSchema)
	AssertJSONEqual(t, modifiedSchema, *simpleTag.JsonSchema, "Listed modified schema should match")

	// === Test 7: Try to create tag with invalid JSON schema (should fail) ===
	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "schema-test",
		Name:        "invalid-schema",
		Description: stringPtr("Tag with invalid schema"),
		JsonSchema:  stringPtr(`{"type": "invalid", "properties": {`), // Invalid JSON
	})
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	// Should return CodeInvalidArgument for invalid JSON schema
	require.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
}
