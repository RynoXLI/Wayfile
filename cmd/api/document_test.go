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
	"time"

	"connectrpc.com/connect"
	documentsv1 "github.com/RynoXLI/Wayfile/gen/go/documents/v1"
	namespacesv1 "github.com/RynoXLI/Wayfile/gen/go/namespaces/v1"
	tagsv1 "github.com/RynoXLI/Wayfile/gen/go/tags/v1"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUploadDocument(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	// Create namespace first
	ctx := context.Background()
	_, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "test-namespace",
	})
	require.NoError(t, err)

	// Create a test file content
	fileContent := []byte("Hello, World! This is a test file.")

	// === Step 1: Upload a file ===
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Create form file with explicit Content-Type header
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="file"; filename="test.txt"`}
	h["Content-Type"] = []string{"text/plain; charset=utf-8"}
	part, err := writer.CreatePart(h)
	require.NoError(t, err)

	_, err = part.Write(fileContent)
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ns/test-namespace/documents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	ta.Router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Upload failed with status %d: %s", w.Code, w.Body.String())
	}

	// Verify correct status code (201 Created, not 200 OK)
	require.Equal(t, http.StatusCreated, w.Code, "Upload should return 201 Created")

	var uploadResponse DocumentResponse
	err = json.NewDecoder(w.Body).Decode(&uploadResponse)
	require.NoError(t, err)

	require.NotEmpty(t, uploadResponse.ID, "Document ID should not be empty")
	require.Equal(t, "test.txt", uploadResponse.FileName, "Filename should match")
	require.NotEmpty(t, uploadResponse.ChecksumSHA, "Checksum should not be empty")
	require.NotEmpty(t, uploadResponse.DownloadURL, "Download URL should be present")
	require.Contains(t, uploadResponse.DownloadURL, "token=", "Download URL should contain token")

	documentID := uploadResponse.ID

	// Note: MIME type verification would be done through API responses in a real implementation
	// For now, we trust the upload succeeded with correct MIME type

	// === Step 2: Download the file and verify content ===
	req = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/ns/test-namespace/documents/"+documentID,
		nil,
	)
	w = httptest.NewRecorder()

	ta.Router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	downloadedContent := w.Body.Bytes()
	require.Equal(
		t,
		fileContent,
		downloadedContent,
		"Downloaded content should match uploaded content",
	)

	// === Step 2b: Test pre-signed download URL with token ===
	req = httptest.NewRequest(http.MethodGet, uploadResponse.DownloadURL, nil)
	w = httptest.NewRecorder()
	ta.Router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "Download with valid token should succeed")
	require.Equal(t, fileContent, w.Body.Bytes(), "Token download content should match")

	// Test token with wrong namespace UUID should fail
	wrongNsUUID := uuid.New().String()
	wrongToken := ta.App.Signer.GenerateToken(wrongNsUUID, documentID, 1*time.Hour)
	req = httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/v1/ns/test-namespace/documents/%s?token=%s", documentID, wrongToken),
		nil,
	)
	w = httptest.NewRecorder()
	ta.Router.ServeHTTP(w, req)
	require.Equal(
		t,
		http.StatusUnauthorized,
		w.Code,
		"Token with wrong namespace UUID should be rejected",
	)

	// === Step 3: Try to upload the same file again (should get 409 Conflict) ===
	body = &bytes.Buffer{}
	writer = multipart.NewWriter(body)

	part, err = writer.CreateFormFile("file", "test.txt")
	require.NoError(t, err)

	_, err = part.Write(fileContent)
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/ns/test-namespace/documents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w = httptest.NewRecorder()

	ta.Router.ServeHTTP(w, req)

	require.Equal(t, http.StatusConflict, w.Code, "Duplicate file should return 409 Conflict")

	// === Step 4: Delete the document via Connect RPC ===
	deleteReq := &documentsv1.DeleteDocumentRequest{
		Namespace:  "test-namespace",
		DocumentId: documentID,
	}
	_, err = ta.ConnectClient.DeleteDocument(ctx, deleteReq)
	require.NoError(t, err, "Delete should succeed via Connect RPC")

	// === Step 5: Try to download the deleted file (should get 404) ===
	req = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/ns/test-namespace/documents/"+documentID,
		nil,
	)
	w = httptest.NewRecorder()

	ta.Router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code, "Deleted file should return 404 Not Found")

	// === Step 6: Upload empty file ===
	emptyContent := []byte{}
	body = &bytes.Buffer{}
	writer = multipart.NewWriter(body)

	part, err = writer.CreateFormFile("file", "empty.txt")
	require.NoError(t, err)

	_, err = part.Write(emptyContent)
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/ns/test-namespace/documents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w = httptest.NewRecorder()

	ta.Router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code, "Empty file should upload successfully")

	var emptyResponse DocumentResponse
	err = json.NewDecoder(w.Body).Decode(&emptyResponse)
	require.NoError(t, err)
	require.Equal(t, "empty.txt", emptyResponse.FileName)

	// === Step 7: Upload multiple documents to same namespace ===
	doc1Content := []byte("Document 1 content")
	body = &bytes.Buffer{}
	writer = multipart.NewWriter(body)

	part, err = writer.CreateFormFile("file", "doc1.txt")
	require.NoError(t, err)
	_, err = part.Write(doc1Content)
	require.NoError(t, err)
	err = writer.Close()
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/ns/test-namespace/documents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w = httptest.NewRecorder()

	ta.Router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var doc1Response DocumentResponse
	err = json.NewDecoder(w.Body).Decode(&doc1Response)
	require.NoError(t, err)

	// Upload second document
	doc2Content := []byte("Document 2 content")
	body = &bytes.Buffer{}
	writer = multipart.NewWriter(body)

	part, err = writer.CreateFormFile("file", "doc2.txt")
	require.NoError(t, err)
	_, err = part.Write(doc2Content)
	require.NoError(t, err)
	err = writer.Close()
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/ns/test-namespace/documents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w = httptest.NewRecorder()

	ta.Router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var doc2Response DocumentResponse
	err = json.NewDecoder(w.Body).Decode(&doc2Response)
	require.NoError(t, err)

	// Verify both documents have different IDs
	require.NotEqual(
		t,
		doc1Response.ID,
		doc2Response.ID,
		"Multiple documents should have unique IDs",
	)

	// Verify both are downloadable
	req = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/ns/test-namespace/documents/"+doc1Response.ID,
		nil,
	)
	w = httptest.NewRecorder()
	ta.Router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, doc1Content, w.Body.Bytes())

	req = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/ns/test-namespace/documents/"+doc2Response.ID,
		nil,
	)
	w = httptest.NewRecorder()
	ta.Router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, doc2Content, w.Body.Bytes())

	// === Step 8: Upload file with special characters in filename ===
	specialFilenames := []string{
		"test file (1).txt",
		"文档.txt",
		"file's name.txt",
		"test-file_2024.txt",
	}

	for i, filename := range specialFilenames {
		// Make content unique for each file to avoid duplicate detection
		specialContent := []byte(fmt.Sprintf("Special filename content %d", i))

		body = &bytes.Buffer{}
		writer = multipart.NewWriter(body)

		part, err = writer.CreateFormFile("file", filename)
		require.NoError(t, err)
		_, err = part.Write(specialContent)
		require.NoError(t, err)
		err = writer.Close()
		require.NoError(t, err)

		req = httptest.NewRequest(http.MethodPost, "/api/v1/ns/test-namespace/documents", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w = httptest.NewRecorder()

		ta.Router.ServeHTTP(w, req)

		require.Equal(
			t,
			http.StatusCreated,
			w.Code,
			"File with special characters should upload: %s",
			filename,
		)

		var specialResponse DocumentResponse
		err = json.NewDecoder(w.Body).Decode(&specialResponse)
		require.NoError(t, err)
		require.Equal(t, filename, specialResponse.FileName, "Filename should be preserved")

		// Verify downloadable
		req = httptest.NewRequest(
			http.MethodGet,
			"/api/v1/ns/test-namespace/documents/"+specialResponse.ID,
			nil,
		)
		w = httptest.NewRecorder()
		ta.Router.ServeHTTP(w, req)
		require.Equal(
			t,
			http.StatusOK,
			w.Code,
			"Should be able to download file with special characters: %s",
			filename,
		)
		require.Equal(t, specialContent, w.Body.Bytes())
	}
}

// TestInvalidDocumentID tests error handling with invalid UUIDs
func TestInvalidDocumentID(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	// Create namespace
	ctx := context.Background()
	_, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "test-namespace",
	})
	require.NoError(t, err)

	// Try to download with invalid UUID
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/ns/test-namespace/documents/not-a-uuid",
		nil,
	)
	w := httptest.NewRecorder()

	ta.Router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "Invalid UUID should return 422")

	// Try to delete with invalid UUID via Connect RPC
	deleteReq := &documentsv1.DeleteDocumentRequest{
		Namespace:  "test-namespace",
		DocumentId: "invalid-id",
	}
	_, err = ta.ConnectClient.DeleteDocument(ctx, deleteReq)
	require.Error(t, err, "Delete with invalid UUID should fail")
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	require.Equal(
		t,
		connect.CodeInvalidArgument,
		connectErr.Code(),
		"Invalid UUID should return InvalidArgument",
	)
}

// TestNamespaceIsolation tests that documents cannot be accessed across namespaces
func TestNamespaceIsolation(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	// Create two namespaces
	ctx := context.Background()
	_, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "namespace-a",
	})
	require.NoError(t, err)
	_, err = ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "namespace-b",
	})
	require.NoError(t, err)

	// Upload file to namespace-a
	fileContent := []byte("Secret content for namespace A")
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", "secret.txt")
	require.NoError(t, err)

	_, err = part.Write(fileContent)
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ns/namespace-a/documents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	ta.Router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var uploadResponse DocumentResponse
	err = json.NewDecoder(w.Body).Decode(&uploadResponse)
	require.NoError(t, err)

	documentID := uploadResponse.ID

	// Try to access the document via namespace-b (should fail)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/ns/namespace-b/documents/"+documentID, nil)
	w = httptest.NewRecorder()

	ta.Router.ServeHTTP(w, req)

	require.Equal(
		t,
		http.StatusNotFound,
		w.Code,
		"Document should not be accessible from different namespace",
	)

	// Try to delete via namespace-b (should fail) via Connect RPC
	deleteReq := &documentsv1.DeleteDocumentRequest{
		Namespace:  "namespace-b",
		DocumentId: documentID,
	}
	_, err = ta.ConnectClient.DeleteDocument(ctx, deleteReq)
	require.Error(t, err, "Delete from different namespace should fail")
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	require.Equal(
		t,
		connect.CodeNotFound,
		connectErr.Code(),
		"Document should not be deletable from different namespace",
	)

	// Verify it's still accessible from namespace-a
	req = httptest.NewRequest(http.MethodGet, "/api/v1/ns/namespace-a/documents/"+documentID, nil)
	w = httptest.NewRecorder()

	ta.Router.ServeHTTP(w, req)

	require.Equal(
		t,
		http.StatusOK,
		w.Code,
		"Document should still be accessible from correct namespace",
	)
	require.Equal(t, fileContent, w.Body.Bytes())
}

// TestUploadErrors tests various error conditions during upload
func TestUploadErrors(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	// Create namespace
	ctx := context.Background()
	_, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "test-namespace",
	})
	require.NoError(t, err)

	// Test missing file in form
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ns/test-namespace/documents", nil)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	w := httptest.NewRecorder()

	ta.Router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "Missing file should return 422")

	// Test upload to non-existent namespace
	fileContent := []byte("Test content")
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", "test.txt")
	require.NoError(t, err)

	_, err = part.Write(fileContent)
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/ns/nonexistent/documents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w = httptest.NewRecorder()

	ta.Router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code, "Non-existent namespace should return 404")
}

func TestDocumentTagAssociation(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	ctx := context.Background()

	// Create namespace
	_, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "tag-assoc-test",
	})
	require.NoError(t, err)

	// Upload a document
	fileContent := []byte("Test document for tag association")
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "test.txt")
	require.NoError(t, err)
	_, err = part.Write(fileContent)
	require.NoError(t, err)
	err = writer.Close()
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ns/tag-assoc-test/documents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	ta.Router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	var uploadResp map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &uploadResp)
	require.NoError(t, err)
	documentID := uploadResp["id"].(string)

	// Create a tag
	tagResp, err := ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "tag-assoc-test",
		Name:        "project",
		Description: stringPtr("Project tag"),
	})
	require.NoError(t, err)
	require.NotNil(t, tagResp.Tag)

	// Add tag to document (without attributes)
	_, err = ta.ConnectClient.AddTagToDocument(ctx, &documentsv1.AddTagToDocumentRequest{
		Namespace:  "tag-assoc-test",
		DocumentId: documentID,
		TagPath:    "/project",
	})
	require.NoError(t, err)

	// Verify tag was added using ListDocumentTags
	listResp, err := ta.ConnectClient.ListDocumentTags(ctx, &documentsv1.ListDocumentTagsRequest{
		Namespace:  "tag-assoc-test",
		DocumentId: documentID,
	})
	require.NoError(t, err)
	require.Len(t, listResp.Tags, 1)
	require.Equal(t, "project", listResp.Tags[0].Name)
	require.Equal(t, "/project", listResp.Tags[0].TagPath)
	require.Nil(t, listResp.Tags[0].Attributes) // No attributes provided

	// Remove tag from document
	_, err = ta.ConnectClient.RemoveTagFromDocument(ctx, &documentsv1.RemoveTagFromDocumentRequest{
		Namespace:  "tag-assoc-test",
		DocumentId: documentID,
		TagPath:    "/project",
	})
	require.NoError(t, err)

	// Verify tag was removed
	listResp, err = ta.ConnectClient.ListDocumentTags(ctx, &documentsv1.ListDocumentTagsRequest{
		Namespace:  "tag-assoc-test",
		DocumentId: documentID,
	})
	require.NoError(t, err)
	require.Len(t, listResp.Tags, 0)
}

func TestDocumentTagWithAttributes(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	ctx := context.Background()

	// Create namespace
	_, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "tag-attr-test",
	})
	require.NoError(t, err)

	// Upload a document
	fileContent := []byte("Test document for tag attributes")
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "test.txt")
	require.NoError(t, err)
	_, err = part.Write(fileContent)
	require.NoError(t, err)
	err = writer.Close()
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ns/tag-attr-test/documents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	ta.Router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	var uploadResp map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &uploadResp)
	require.NoError(t, err)
	documentID := uploadResp["id"].(string)

	// Create a tag with an attribute schema
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"amount": map[string]interface{}{
				"type": "number",
			},
			"currency": map[string]interface{}{
				"type": "string",
			},
		},
		"required": []string{"amount", "currency"},
	}
	schemaJSON, err := json.Marshal(schema)
	require.NoError(t, err)

	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "tag-attr-test",
		Name:        "invoice",
		Description: stringPtr("Invoice tag with attributes"),
		JsonSchema:  stringPtr(string(schemaJSON)),
	})
	require.NoError(t, err)

	// Try to add tag with valid attributes
	validAttrs := `{"amount": 100.50, "currency": "USD"}`
	_, err = ta.ConnectClient.AddTagToDocument(ctx, &documentsv1.AddTagToDocumentRequest{
		Namespace:  "tag-attr-test",
		DocumentId: documentID,
		TagPath:    "/invoice",
		Attributes: &validAttrs,
	})
	require.NoError(t, err)

	// Verify tag was added with correct attributes
	tagAttrs, err := ta.ConnectClient.GetDocumentAttributes(
		ctx,
		&documentsv1.GetDocumentAttributesRequest{
			Namespace:  "tag-attr-test",
			DocumentId: documentID,
			TagPath:    &[]string{"/invoice"}[0],
		},
	)
	require.NoError(t, err)
	require.NotNil(t, tagAttrs.Attributes)
	require.Equal(t, validAttrs, *tagAttrs.Attributes)
	require.NotNil(t, tagAttrs.Metadata)

	// Also verify through ListDocumentTags
	listResp, err := ta.ConnectClient.ListDocumentTags(ctx, &documentsv1.ListDocumentTagsRequest{
		Namespace:  "tag-attr-test",
		DocumentId: documentID,
	})
	require.NoError(t, err)
	require.Len(t, listResp.Tags, 1)
	require.Equal(t, "invoice", listResp.Tags[0].Name)
	require.Equal(t, "/invoice", listResp.Tags[0].TagPath)
	require.NotNil(t, listResp.Tags[0].Attributes)
	require.Equal(t, validAttrs, *listResp.Tags[0].Attributes)

	// Remove the tag to test invalid attributes
	_, err = ta.ConnectClient.RemoveTagFromDocument(ctx, &documentsv1.RemoveTagFromDocumentRequest{
		Namespace:  "tag-attr-test",
		DocumentId: documentID,
		TagPath:    "/invoice",
	})
	require.NoError(t, err)

	// Try to add tag with invalid attributes (should fail)
	invalidAttrs := `{"amount": "not-a-number", "currency": "USD"}`
	_, err = ta.ConnectClient.AddTagToDocument(ctx, &documentsv1.AddTagToDocumentRequest{
		Namespace:  "tag-attr-test",
		DocumentId: documentID,
		TagPath:    "/invoice",
		Attributes: &invalidAttrs,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "validation failed")
}

func TestUpdateDocumentAttributes(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	ctx := context.Background()

	// Create namespace
	_, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "update-attrs-test",
	})
	require.NoError(t, err)

	// Upload a document
	fileContent := []byte("Test document for updating tag attributes")
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "update-test.txt")
	require.NoError(t, err)
	_, err = part.Write(fileContent)
	require.NoError(t, err)
	err = writer.Close()
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ns/update-attrs-test/documents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	ta.Router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	var uploadResp map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &uploadResp)
	require.NoError(t, err)
	documentID := uploadResp["id"].(string)

	// Create a tag with schema
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"type": map[string]interface{}{
				"type": "string",
				"enum": []string{"report", "invoice", "receipt"},
			},
			"priority": map[string]interface{}{
				"type":    "integer",
				"minimum": 1,
				"maximum": 10,
			},
			"description": map[string]interface{}{
				"type":      "string",
				"minLength": 1,
				"maxLength": 500,
			},
			"status": map[string]interface{}{
				"type": "string",
				"enum": []string{"draft", "review", "approved", "archived"},
			},
		},
		"required": []string{"type", "priority"},
	}
	schemaJSON, err := json.Marshal(schema)
	require.NoError(t, err)

	_, err = ta.TagClient.CreateTag(ctx, &tagsv1.CreateTagRequest{
		Namespace:   "update-attrs-test",
		Name:        "category",
		Description: stringPtr("Category tag for testing updates"),
		JsonSchema:  stringPtr(string(schemaJSON)),
	})
	require.NoError(t, err)

	// Add initial tag with attributes
	initialAttrs := `{"type": "report", "priority": 1, "description": "initial description"}`
	_, err = ta.ConnectClient.AddTagToDocument(ctx, &documentsv1.AddTagToDocumentRequest{
		Namespace:  "update-attrs-test",
		DocumentId: documentID,
		TagPath:    "/category",
		Attributes: &initialAttrs,
	})
	require.NoError(t, err)

	// Verify initial attributes
	getResp, err := ta.ConnectClient.GetDocumentAttributes(
		ctx,
		&documentsv1.GetDocumentAttributesRequest{
			Namespace:  "update-attrs-test",
			DocumentId: documentID,
			TagPath:    &[]string{"/category"}[0],
		},
	)
	require.NoError(t, err)
	require.Equal(t, initialAttrs, *getResp.Attributes)

	// Update the attributes
	updatedAttrs := `{"type": "invoice", "priority": 5, "description": "updated description", "status": "review"}`
	_, err = ta.ConnectClient.UpdateDocumentAttributes(
		ctx,
		&documentsv1.UpdateDocumentAttributesRequest{
			Namespace:  "update-attrs-test",
			DocumentId: documentID,
			TagPath:    &[]string{"/category"}[0],
			Attributes: updatedAttrs,
		},
	)
	require.NoError(t, err)

	// Verify attributes were updated
	getResp, err = ta.ConnectClient.GetDocumentAttributes(
		ctx,
		&documentsv1.GetDocumentAttributesRequest{
			Namespace:  "update-attrs-test",
			DocumentId: documentID,
			TagPath:    &[]string{"/category"}[0],
		},
	)
	require.NoError(t, err)

	// Parse and compare JSON objects instead of strings (order can vary)
	var expectedAttrs, actualAttrs map[string]interface{}
	err = json.Unmarshal([]byte(updatedAttrs), &expectedAttrs)
	require.NoError(t, err)
	err = json.Unmarshal([]byte(*getResp.Attributes), &actualAttrs)
	require.NoError(t, err)
	require.Equal(t, expectedAttrs, actualAttrs)

	// Verify metadata indicates manual extraction and updated timestamp is present
	listResp, err := ta.ConnectClient.ListDocumentTags(ctx, &documentsv1.ListDocumentTagsRequest{
		Namespace:  "update-attrs-test",
		DocumentId: documentID,
	})
	require.NoError(t, err)
	require.Len(t, listResp.Tags, 1)
	require.NotEmpty(t, listResp.Tags[0].UpdatedAt)

	// Parse and verify extraction method from metadata
	require.NotNil(t, listResp.Tags[0].Metadata, "metadata should be present after update")
	var metadata map[string]interface{}
	err = json.Unmarshal([]byte(*listResp.Tags[0].Metadata), &metadata)
	require.NoError(t, err)

	// The update operation should set extraction method to manual for the tag
	tagInfo, ok := metadata["tag"].(map[string]interface{})
	require.True(t, ok, "metadata should have tag info")
	require.Equal(t, "manual", tagInfo["extraction_method"])

	// Test updating with invalid attributes (should fail)
	invalidAttrs := `{"type": "invalid-type", "priority": 15}`
	_, err = ta.ConnectClient.UpdateDocumentAttributes(
		ctx,
		&documentsv1.UpdateDocumentAttributesRequest{
			Namespace:  "update-attrs-test",
			DocumentId: documentID,
			TagPath:    &[]string{"/category"}[0],
			Attributes: invalidAttrs,
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "validation failed")

	// Verify original valid attributes are still there (update should have failed)
	getResp, err = ta.ConnectClient.GetDocumentAttributes(
		ctx,
		&documentsv1.GetDocumentAttributesRequest{
			Namespace:  "update-attrs-test",
			DocumentId: documentID,
			TagPath:    &[]string{"/category"}[0],
		},
	)
	require.NoError(t, err)

	// Parse and compare JSON objects
	var currentAttrs map[string]interface{}
	err = json.Unmarshal([]byte(*getResp.Attributes), &currentAttrs)
	require.NoError(t, err)
	require.Equal(t, expectedAttrs, currentAttrs)

	// Test updating non-existent tag (should fail)
	_, err = ta.ConnectClient.UpdateDocumentAttributes(
		ctx,
		&documentsv1.UpdateDocumentAttributesRequest{
			Namespace:  "update-attrs-test",
			DocumentId: documentID,
			TagPath:    &[]string{"/nonexistent"}[0],
			Attributes: `{"type": "report", "priority": 1}`,
		},
	)
	require.Error(t, err)
}

func stringPtr(s string) *string {
	return &s
}
