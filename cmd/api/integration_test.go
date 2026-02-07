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
	"time"

	"connectrpc.com/connect"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/tern/v2/migrate"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/RynoXLI/Wayfile/cmd/api/rpc"
	documentsv1 "github.com/RynoXLI/Wayfile/gen/go/documents/v1"
	"github.com/RynoXLI/Wayfile/gen/go/documents/v1/documentsv1connect"
	namespacesv1 "github.com/RynoXLI/Wayfile/gen/go/namespaces/v1"
	"github.com/RynoXLI/Wayfile/gen/go/namespaces/v1/namespacesv1connect"
	"github.com/RynoXLI/Wayfile/internal/auth"
	"github.com/RynoXLI/Wayfile/internal/config"
	"github.com/RynoXLI/Wayfile/internal/db/sqlc"
	"github.com/RynoXLI/Wayfile/internal/events"
	"github.com/RynoXLI/Wayfile/internal/middleware"
	"github.com/RynoXLI/Wayfile/internal/services"
	"github.com/RynoXLI/Wayfile/internal/storage"
)

// testApp holds all the test dependencies
type testApp struct {
	app             *App
	router          http.Handler
	pool            *pgxpool.Pool
	nc              *nats.Conn
	tmpDir          string
	pgContainer     *postgres.PostgresContainer
	natsServer      *server.Server
	connectClient   documentsv1connect.DocumentServiceClient
	namespaceClient namespacesv1connect.NamespaceServiceClient
	testServer      *httptest.Server
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
			BaseURL:        baseURL,
		},
	}

	// Setup router with Huma
	router := chi.NewRouter()
	router.Use(chimiddleware.RequestID)
	router.Use(chimiddleware.Logger)
	router.Use(chimiddleware.Recoverer)
	router.Use(middleware.RateLimiter(testCfg.Server.RateLimitRPS, testCfg.Server.RateLimitBurst))

	// Apply max upload size to POST routes only
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				r.Body = http.MaxBytesReader(w, r.Body, testCfg.Server.MaxUploadSize)
			}
			next.ServeHTTP(w, r)
		})
	})

	// Create Huma API
	humaConfig := huma.DefaultConfig("Wayfile Document API", "0.1.0")
	humaConfig.Servers = []*huma.Server{
		{URL: baseURL},
	}
	api := humachi.New(router, humaConfig)

	// Register all routes
	RegisterRoutes(api, app)

	// Mount Connect RPC handlers
	documentsRPCService := rpc.NewDocumentsServiceServer(documentService)
	connectPath, connectHandler := documentsv1connect.NewDocumentServiceHandler(
		documentsRPCService,
		connect.WithInterceptors(),
	)
	router.Mount(connectPath, connectHandler)

	// Mount Namespace RPC handlers
	namespaceRPCService := rpc.NewNamespaceServiceServer(queries)
	namespacePath, namespaceHandler := namespacesv1connect.NewNamespaceServiceHandler(
		namespaceRPCService,
		connect.WithInterceptors(),
	)
	router.Mount(namespacePath, namespaceHandler)

	// Wrap with h2c for HTTP/2
	h2cHandler := h2c.NewHandler(router, &http2.Server{})

	// Start test HTTP server
	testServer := httptest.NewServer(h2cHandler)

	// Create Connect RPC clients using test server URL
	connectClient := documentsv1connect.NewDocumentServiceClient(
		http.DefaultClient,
		testServer.URL,
	)
	namespaceClient := namespacesv1connect.NewNamespaceServiceClient(
		http.DefaultClient,
		testServer.URL,
	)

	return &testApp{
		app:             app,
		router:          h2cHandler,
		pool:            pool,
		nc:              nc,
		tmpDir:          tmpDir,
		pgContainer:     pgContainer,
		natsServer:      natsServer,
		connectClient:   connectClient,
		namespaceClient: namespaceClient,
		testServer:      testServer,
	}
}

// cleanup tears down test resources
func (ta *testApp) cleanup(t *testing.T) {
	ctx := context.Background()

	if ta.testServer != nil {
		ta.testServer.Close()
	}

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

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	ta.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var response struct {
		Status string `json:"status"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	require.Equal(t, "ok", response.Status)
}

// TestUploadDocument tests uploading a document
func TestUploadDocument(t *testing.T) {
	ta := setupTestApp(t)
	defer ta.cleanup(t)

	// Create namespace first
	ctx := context.Background()
	_, err := ta.namespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
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

	ta.router.ServeHTTP(w, req)

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

	// Verify MIME type was stored correctly
	queries := sqlc.New(ta.pool)
	docUUID, err := uuid.Parse(documentID)
	require.NoError(t, err)
	doc, err := queries.GetDocumentByID(ctx, pgtype.UUID{Bytes: docUUID, Valid: true})
	require.NoError(t, err)
	require.Equal(t, "text/plain; charset=utf-8", doc.MimeType, "MIME type should be text/plain")

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

	// === Step 2b: Test pre-signed download URL with token ===
	req = httptest.NewRequest(http.MethodGet, uploadResponse.DownloadURL, nil)
	w = httptest.NewRecorder()
	ta.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "Download with valid token should succeed")
	require.Equal(t, fileContent, w.Body.Bytes(), "Token download content should match")

	// Test token with wrong namespace UUID should fail
	wrongNsUUID := uuid.New().String()
	wrongToken := ta.app.signer.GenerateToken(wrongNsUUID, documentID, 1*time.Hour)
	req = httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/v1/ns/test-namespace/documents/%s?token=%s", documentID, wrongToken),
		nil,
	)
	w = httptest.NewRecorder()
	ta.router.ServeHTTP(w, req)
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

	ta.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusConflict, w.Code, "Duplicate file should return 409 Conflict")

	// === Step 4: Delete the document via Connect RPC ===
	deleteReq := &documentsv1.DeleteDocumentRequest{
		Namespace:  "test-namespace",
		DocumentId: documentID,
	}
	_, err = ta.connectClient.DeleteDocument(ctx, deleteReq)
	require.NoError(t, err, "Delete should succeed via Connect RPC")

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
	_, err := ta.namespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
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

	ta.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "Invalid UUID should return 422")

	// Try to delete with invalid UUID via Connect RPC
	deleteReq := &documentsv1.DeleteDocumentRequest{
		Namespace:  "test-namespace",
		DocumentId: "invalid-id",
	}
	_, err = ta.connectClient.DeleteDocument(ctx, deleteReq)
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
	ta := setupTestApp(t)
	defer ta.cleanup(t)

	// Create two namespaces
	ctx := context.Background()
	_, err := ta.namespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "namespace-a",
	})
	require.NoError(t, err)
	_, err = ta.namespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
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

	// Try to delete via namespace-b (should fail) via Connect RPC
	deleteReq := &documentsv1.DeleteDocumentRequest{
		Namespace:  "namespace-b",
		DocumentId: documentID,
	}
	_, err = ta.connectClient.DeleteDocument(ctx, deleteReq)
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
	_, err := ta.namespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "test-namespace",
	})
	require.NoError(t, err)

	// Test missing file in form
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ns/test-namespace/documents", nil)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	w := httptest.NewRecorder()

	ta.router.ServeHTTP(w, req)

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

	ta.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code, "Non-existent namespace should return 404")
}

// TestNamespaceCRUD tests the namespace CRUD operations via Connect RPC
func TestNamespaceCRUD(t *testing.T) {
	ta := setupTestApp(t)
	defer ta.cleanup(t)

	ctx := context.Background()

	// === Step 1: Create a namespace ===
	createResp, err := ta.namespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "test-namespace-1",
	})
	require.NoError(t, err)
	require.NotNil(t, createResp)
	require.NotNil(t, createResp.Namespace)
	require.Equal(t, "test-namespace-1", createResp.Namespace.Name)
	require.NotEmpty(t, createResp.Namespace.Id)
	require.NotNil(t, createResp.Namespace.CreatedAt)
	require.NotNil(t, createResp.Namespace.ModifiedAt)

	// === Step 2: Create another namespace ===
	createResp2, err := ta.namespaceClient.CreateNamespace(
		ctx,
		&namespacesv1.CreateNamespaceRequest{
			Name: "test-namespace-2",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, createResp2)
	require.NotNil(t, createResp2.Namespace)
	require.Equal(t, "test-namespace-2", createResp2.Namespace.Name)

	// === Step 3: Get all namespaces ===
	listResp, err := ta.namespaceClient.GetNamespaces(ctx, &namespacesv1.GetNamespacesRequest{})
	require.NoError(t, err)
	require.NotNil(t, listResp)
	require.GreaterOrEqual(t, len(listResp.Namespaces), 2, "Should have at least 2 namespaces")

	// Check that our namespaces are in the list
	foundNs1 := false
	foundNs2 := false
	for _, ns := range listResp.Namespaces {
		if ns.Name == "test-namespace-1" {
			foundNs1 = true
		}
		if ns.Name == "test-namespace-2" {
			foundNs2 = true
		}
	}
	require.True(t, foundNs1, "test-namespace-1 should be in the list")
	require.True(t, foundNs2, "test-namespace-2 should be in the list")

	// === Step 4: Get specific namespace by name ===
	getResp, err := ta.namespaceClient.GetNamespace(ctx, &namespacesv1.GetNamespaceRequest{
		Name: "test-namespace-1",
	})
	require.NoError(t, err)
	require.NotNil(t, getResp)
	require.NotNil(t, getResp.Namespace)
	require.Equal(t, "test-namespace-1", getResp.Namespace.Name)
	require.Equal(t, createResp.Namespace.Id, getResp.Namespace.Id)

	// === Step 5: Try to get non-existent namespace ===
	_, err = ta.namespaceClient.GetNamespace(ctx, &namespacesv1.GetNamespaceRequest{
		Name: "nonexistent",
	})
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	require.Equal(
		t,
		connect.CodeNotFound,
		connectErr.Code(),
		"Non-existent namespace should return NotFound",
	)

	// === Step 6: Delete a namespace ===
	deleteResp, err := ta.namespaceClient.DeleteNamespace(ctx, &namespacesv1.DeleteNamespaceRequest{
		Name: "test-namespace-1",
	})
	require.NoError(t, err)
	require.NotNil(t, deleteResp)

	// === Step 7: Verify namespace was deleted ===
	_, err = ta.namespaceClient.GetNamespace(ctx, &namespacesv1.GetNamespaceRequest{
		Name: "test-namespace-1",
	})
	require.Error(t, err)
	require.ErrorAs(t, err, &connectErr)
	require.Equal(
		t,
		connect.CodeNotFound,
		connectErr.Code(),
		"Deleted namespace should not be found",
	)

	// === Step 8: Verify other namespace still exists ===
	getResp2, err := ta.namespaceClient.GetNamespace(ctx, &namespacesv1.GetNamespaceRequest{
		Name: "test-namespace-2",
	})
	require.NoError(t, err)
	require.NotNil(t, getResp2)
	require.NotNil(t, getResp2.Namespace)
	require.Equal(t, "test-namespace-2", getResp2.Namespace.Name)

	// === Step 9: Test validation - empty namespace name on create ===
	_, err = ta.namespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "",
	})
	require.Error(t, err)
	require.ErrorAs(t, err, &connectErr)
	require.Equal(
		t,
		connect.CodeInvalidArgument,
		connectErr.Code(),
		"Empty name should return InvalidArgument",
	)

	// === Step 10: Test validation - empty namespace name on get ===
	_, err = ta.namespaceClient.GetNamespace(ctx, &namespacesv1.GetNamespaceRequest{
		Name: "",
	})
	require.Error(t, err)
	require.ErrorAs(t, err, &connectErr)
	require.Equal(
		t,
		connect.CodeInvalidArgument,
		connectErr.Code(),
		"Empty name should return InvalidArgument",
	)

	// === Step 11: Test validation - empty namespace name on delete ===
	_, err = ta.namespaceClient.DeleteNamespace(ctx, &namespacesv1.DeleteNamespaceRequest{
		Name: "",
	})
	require.Error(t, err)
	require.ErrorAs(t, err, &connectErr)
	require.Equal(
		t,
		connect.CodeInvalidArgument,
		connectErr.Code(),
		"Empty name should return InvalidArgument",
	)
}
