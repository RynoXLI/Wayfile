//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/RynoXLI/Wayfile/internal/auth"
	"github.com/RynoXLI/Wayfile/internal/config"
	"github.com/RynoXLI/Wayfile/internal/db/sqlc"
	"github.com/RynoXLI/Wayfile/internal/events"
	"github.com/RynoXLI/Wayfile/internal/services"
	"github.com/RynoXLI/Wayfile/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/tern/v2/migrate"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// testApp holds all the test dependencies
type testApp struct {
	app         *App
	router      chi.Router
	pool        *pgxpool.Pool
	nc          *nats.Conn
	tmpDir      string
	pgContainer *postgres.PostgresContainer
	natsServer  *server.Server
}

// setupTestApp initializes the application for integration testing
func setupTestApp(t *testing.T) *testApp {
	// Create logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx := context.Background()

	// Start PostgreSQL container
	pgContainer, err := postgres.Run(context.Background(),
		"pgvector/pgvector:pg16",
		postgres.WithDatabase("wayfile_test"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2),
		),
	)
	require.NoError(t, err)

	// Get database connection string
	dbURL, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Connect to test database
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)

	// Run database migrations
	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	migrator, err := migrate.NewMigrator(ctx, conn.Conn(), "schema_version")
	require.NoError(t, err)

	// Load migrations from the migrations directory
	migrationsDir := filepath.Join("..", "..", "migrations")
	err = migrator.LoadMigrations(os.DirFS(migrationsDir))
	require.NoError(t, err)

	// Run migrations
	err = migrator.Migrate(ctx)
	require.NoError(t, err)

	// Start embedded NATS server with JetStream
	natsOpts := &server.Options{
		JetStream: true,
		StoreDir:  t.TempDir(),
		Port:      -1, // Random port
	}
	natsServer, err := server.NewServer(natsOpts)
	require.NoError(t, err)

	go natsServer.Start()
	require.True(t, natsServer.ReadyForConnections(10e9)) // 10 second timeout

	// Connect to NATS
	nc, err := nats.Connect(natsServer.ClientURL())
	require.NoError(t, err)

	js, err := nc.JetStream()
	require.NoError(t, err)

	// Create temporary storage directory
	tmpDir, err := os.MkdirTemp("", "wayfile-test-*")
	require.NoError(t, err)

	// Initialize storage client
	localClient, err := storage.NewLocalStorage(tmpDir, logger)
	require.NoError(t, err)

	// Initialize event publisher and storage
	publisher := events.NewPublisher(js)
	queries := sqlc.New(pool)
	storageService := storage.NewStorage(localClient, queries, logger)

	// Initialize document service
	signer := auth.NewSigner("test-secret")
	baseURL := "http://localhost:8080"
	documentService := services.NewDocumentService(storageService, publisher, signer, baseURL)

	// Initialize app
	app := &App{
		documentService: documentService,
		logger:          logger,
		signer:          signer,
		baseURL:         baseURL,
		pool:            pool,
		nc:              nc,
	}

	// Create test config
	testCfg := &config.Config{
		Server: config.ServerConfig{
			RateLimitRPS:   1000,
			RateLimitBurst: 2000,
			MaxUploadSize:  104857600, // 100 MB
		},
	}

	r := ChiRouter(app, testCfg)

	return &testApp{
		app:         app,
		router:      r,
		pool:        pool,
		nc:          nc,
		tmpDir:      tmpDir,
		pgContainer: pgContainer,
		natsServer:  natsServer,
	}
}

// cleanup tears down test resources
func (ta *testApp) cleanup(t *testing.T) {
	ctx := context.Background()

	ta.nc.Close()
	ta.pool.Close()

	if ta.natsServer != nil {
		ta.natsServer.Shutdown()
	}

	if err := ta.pgContainer.Terminate(ctx); err != nil {
		t.Logf("failed to terminate postgres container: %s", err)
	}

	if err := os.RemoveAll(ta.tmpDir); err != nil {
		t.Logf("failed to remove temp dir: %s", err)
	}
}

// TestHealthEndpoint tests the health check endpoint
func TestHealthEndpoint(t *testing.T) {
	ta := setupTestApp(t)
	defer ta.cleanup(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	w := httptest.NewRecorder()

	ta.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "OK", w.Body.String())
}

// TestUploadDocument tests uploading a document
func TestUploadDocument(t *testing.T) {
	ta := setupTestApp(t)
	defer ta.cleanup(t)

	// Create namespace first
	ctx := context.Background()
	queries := sqlc.New(ta.pool)
	_, err := queries.CreateNamespace(ctx, "test-namespace")
	require.NoError(t, err)

	// Create a test file content
	fileContent := []byte("Hello, World! This is a test file.")

	// === Step 1: Upload a file ===
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", "test.txt")
	require.NoError(t, err)

	_, err = part.Write(fileContent)
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ns/test-namespace/documents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	ta.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var uploadResponse DocumentResponse
	err = json.NewDecoder(w.Body).Decode(&uploadResponse)
	require.NoError(t, err)

	require.NotEmpty(t, uploadResponse.ID, "Document ID should not be empty")
	require.Equal(t, "test.txt", uploadResponse.FileName, "Filename should match")
	require.NotEmpty(t, uploadResponse.ChecksumSHA, "Checksum should not be empty")

	documentID := uploadResponse.ID

	// === Step 2: Download the file and verify content ===
	req = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/ns/test-namespace/documents/"+documentID,
		nil,
	)
	w = httptest.NewRecorder()

	ta.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	downloadedContent := w.Body.Bytes()
	require.Equal(
		t,
		fileContent,
		downloadedContent,
		"Downloaded content should match uploaded content",
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

	ta.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusConflict, w.Code, "Duplicate file should return 409 Conflict")

	// === Step 4: Delete the document ===
	req = httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/ns/test-namespace/documents/"+documentID,
		nil,
	)
	w = httptest.NewRecorder()

	ta.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code, "Delete should return 204 No Content")

	// === Step 5: Try to download the deleted file (should get 404) ===
	req = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/ns/test-namespace/documents/"+documentID,
		nil,
	)
	w = httptest.NewRecorder()

	ta.router.ServeHTTP(w, req)

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

	ta.router.ServeHTTP(w, req)

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

	ta.router.ServeHTTP(w, req)

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

	ta.router.ServeHTTP(w, req)

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
	ta.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, doc1Content, w.Body.Bytes())

	req = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/ns/test-namespace/documents/"+doc2Response.ID,
		nil,
	)
	w = httptest.NewRecorder()
	ta.router.ServeHTTP(w, req)
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

		ta.router.ServeHTTP(w, req)

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
		ta.router.ServeHTTP(w, req)
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
	ta := setupTestApp(t)
	defer ta.cleanup(t)

	// Create namespace
	ctx := context.Background()
	queries := sqlc.New(ta.pool)
	_, err := queries.CreateNamespace(ctx, "test-namespace")
	require.NoError(t, err)

	// Try to download with invalid UUID
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/ns/test-namespace/documents/not-a-uuid",
		nil,
	)
	w := httptest.NewRecorder()

	ta.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code, "Invalid UUID should return 404")

	// Try to delete with invalid UUID
	req = httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/ns/test-namespace/documents/invalid-id",
		nil,
	)
	w = httptest.NewRecorder()

	ta.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code, "Invalid UUID should return 404")
}

// TestNamespaceIsolation tests that documents cannot be accessed across namespaces
func TestNamespaceIsolation(t *testing.T) {
	ta := setupTestApp(t)
	defer ta.cleanup(t)

	// Create two namespaces
	ctx := context.Background()
	queries := sqlc.New(ta.pool)
	_, err := queries.CreateNamespace(ctx, "namespace-a")
	require.NoError(t, err)
	_, err = queries.CreateNamespace(ctx, "namespace-b")
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

	ta.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var uploadResponse DocumentResponse
	err = json.NewDecoder(w.Body).Decode(&uploadResponse)
	require.NoError(t, err)

	documentID := uploadResponse.ID

	// Try to access the document via namespace-b (should fail)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/ns/namespace-b/documents/"+documentID, nil)
	w = httptest.NewRecorder()

	ta.router.ServeHTTP(w, req)

	require.Equal(
		t,
		http.StatusNotFound,
		w.Code,
		"Document should not be accessible from different namespace",
	)

	// Try to delete via namespace-b (should fail)
	req = httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/ns/namespace-b/documents/"+documentID,
		nil,
	)
	w = httptest.NewRecorder()

	ta.router.ServeHTTP(w, req)

	require.Equal(
		t,
		http.StatusNotFound,
		w.Code,
		"Document should not be deletable from different namespace",
	)

	// Verify it's still accessible from namespace-a
	req = httptest.NewRequest(http.MethodGet, "/api/v1/ns/namespace-a/documents/"+documentID, nil)
	w = httptest.NewRecorder()

	ta.router.ServeHTTP(w, req)

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
	ta := setupTestApp(t)
	defer ta.cleanup(t)

	// Create namespace
	ctx := context.Background()
	queries := sqlc.New(ta.pool)
	_, err := queries.CreateNamespace(ctx, "test-namespace")
	require.NoError(t, err)

	// Test missing file in form
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ns/test-namespace/documents", nil)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	w := httptest.NewRecorder()

	ta.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code, "Missing file should return 400")

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

	ta.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code, "Non-existent namespace should return 404")
}
