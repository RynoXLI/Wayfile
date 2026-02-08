//go:build integration

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
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
	"github.com/RynoXLI/Wayfile/gen/go/documents/v1/documentsv1connect"
	"github.com/RynoXLI/Wayfile/gen/go/namespaces/v1/namespacesv1connect"
	"github.com/RynoXLI/Wayfile/gen/go/tags/v1/tagsv1connect"
	"github.com/RynoXLI/Wayfile/internal/auth"
	"github.com/RynoXLI/Wayfile/internal/config"
	"github.com/RynoXLI/Wayfile/internal/db/sqlc"
	"github.com/RynoXLI/Wayfile/internal/events"
	"github.com/RynoXLI/Wayfile/internal/middleware"
	"github.com/RynoXLI/Wayfile/internal/services"
	"github.com/RynoXLI/Wayfile/internal/storage"
)

// sharedTestResources holds resources shared across all tests
type sharedTestResources struct {
	pgContainer *postgres.PostgresContainer
	natsServer  *server.Server
	dbURL       string
	natsURL     string
	natsDir     string
	once        sync.Once
	mu          sync.Mutex
}

var shared = &sharedTestResources{}

// TestMain runs before all tests to set up shared resources and migrations
func TestMain(m *testing.M) {
	var code int
	defer func() {
		// Cleanup shared resources after all tests
		if shared.natsServer != nil {
			shared.natsServer.Shutdown()
			if shared.natsDir != "" {
				os.RemoveAll(shared.natsDir)
			}
		}
		if shared.pgContainer != nil {
			if err := shared.pgContainer.Terminate(context.Background()); err != nil {
				slog.Error("Failed to terminate postgres container", "error", err)
			}
		}
		os.Exit(code)
	}()

	code = m.Run()
}

// runMigrationsOnce runs database migrations once on the shared container
func runMigrationsOnce(t *testing.T, dbURL string) {
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	defer pool.Close()

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
}

// getSharedResources initializes shared containers once for all tests
func getSharedResources(
	t *testing.T,
) (pgContainer *postgres.PostgresContainer, natsServer *server.Server, dbURL, natsURL string) {
	shared.once.Do(func() {
		ctx := context.Background()

		// Start PostgreSQL container once
		pgContainer, err := postgres.Run(ctx,
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

		dbURL, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
		require.NoError(t, err)

		// Create temp directory for NATS JetStream
		natsDir, err := os.MkdirTemp("", "wayfile-nats-*")
		require.NoError(t, err)

		// Start embedded NATS server once with JetStream
		natsOpts := &server.Options{
			JetStream: true,
			StoreDir:  natsDir,
			Port:      -1, // Random port
		}
		natsServer, err := server.NewServer(natsOpts)
		require.NoError(t, err)

		go natsServer.Start()
		require.True(t, natsServer.ReadyForConnections(10e9)) // 10 second timeout

		shared.pgContainer = pgContainer
		shared.natsServer = natsServer
		shared.dbURL = dbURL
		shared.natsURL = natsServer.ClientURL()
		shared.natsDir = natsDir

		// Run migrations once on shared database
		runMigrationsOnce(t, dbURL)
	})

	return shared.pgContainer, shared.natsServer, shared.dbURL, shared.natsURL
}

// TestApp holds all the test dependencies
type TestApp struct {
	App             *App
	Router          http.Handler
	Pool            *pgxpool.Pool
	NC              *nats.Conn
	TmpDir          string
	PgContainer     *postgres.PostgresContainer
	NatsServer      *server.Server
	ConnectClient   documentsv1connect.DocumentServiceClient
	NamespaceClient namespacesv1connect.NamespaceServiceClient
	TagClient       tagsv1connect.TagServiceClient
	TestServer      *httptest.Server
}

// SetupTestApp initializes the application for integration testing.
// Each test gets:
// - Fresh database state (all tables truncated except schema_version)
// - Fresh NATS state (all streams and consumers deleted)
// - Fresh storage directory (new temp directory per test)
// - Fresh connection pool and NATS connection
func SetupTestApp(t *testing.T) *TestApp {
	// Get shared containers (created once per test suite)
	_, _, dbURL, natsURL := getSharedResources(t)

	// Lock to prevent concurrent test setup/cleanup
	shared.mu.Lock()
	defer shared.mu.Unlock()

	// Create logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx := context.Background()

	// Connect to shared test database with fresh connection pool
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)

	// Clean database before each test
	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)

	_, err = conn.Exec(ctx, `
		DO $$ DECLARE
			r RECORD;
		BEGIN
			FOR r IN (SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename != 'schema_version') LOOP
				EXECUTE 'TRUNCATE TABLE ' || quote_ident(r.tablename) || ' RESTART IDENTITY CASCADE';
			END LOOP;
		END $$;
	`)
	require.NoError(t, err)
	conn.Release()

	// Connect to shared NATS server with fresh connection
	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)

	js, err := nc.JetStream()
	require.NoError(t, err)

	// Delete all streams to clean NATS state
	for stream := range js.StreamNames() {
		_ = js.DeleteStream(stream)
	}

	// Create temporary storage directory for this test
	tmpDir, err := os.MkdirTemp("", "wayfile-test-*")
	require.NoError(t, err)

	// Initialize storage client
	localClient, err := storage.NewLocalStorage(tmpDir, logger)
	require.NoError(t, err)

	// Initialize event publisher and storage
	publisher := events.NewPublisher(js)
	queries := sqlc.New(pool)
	storageService := storage.NewStorage(localClient, queries, logger)

	// Initialize tag service (needed by document service)
	tagService := services.NewTagService(queries, publisher)

	// Initialize document service
	signer := auth.NewSigner("test-secret")
	baseURL := "http://localhost:8080"
	documentService := services.NewDocumentService(
		storageService,
		publisher,
		signer,
		baseURL,
		queries,
		tagService,
	)

	// Initialize namespace service
	namespaceService := services.NewNamespaceService(queries)

	// Initialize app (need to export fields in main.go App struct)
	app := &App{
		DocumentService: documentService,
		Logger:          logger,
		Signer:          signer,
		BaseURL:         baseURL,
		Pool:            pool,
		NC:              nc,
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
	humaAPI := humachi.New(router, humaConfig)

	// Register all routes
	RegisterRoutes(humaAPI, app)

	// Mount Connect RPC handlers
	documentsRPCService := rpc.NewDocumentsServiceServer(documentService)
	connectPath, connectHandler := documentsv1connect.NewDocumentServiceHandler(
		documentsRPCService,
		connect.WithInterceptors(),
	)
	router.Mount(connectPath, connectHandler)

	// Mount Namespace RPC handlers
	namespaceRPCService := rpc.NewNamespaceServiceServer(namespaceService)
	namespacePath, namespaceHandler := namespacesv1connect.NewNamespaceServiceHandler(
		namespaceRPCService,
		connect.WithInterceptors(),
	)
	router.Mount(namespacePath, namespaceHandler)

	// Mount Tag RPC handlers
	tagRPCService := rpc.NewTagServiceServer(tagService)
	tagPath, tagHandler := tagsv1connect.NewTagServiceHandler(
		tagRPCService,
		connect.WithInterceptors(),
	)
	router.Mount(tagPath, tagHandler)

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
	tagClient := tagsv1connect.NewTagServiceClient(
		http.DefaultClient,
		testServer.URL,
	)

	return &TestApp{
		App:             app,
		Router:          h2cHandler,
		Pool:            pool,
		NC:              nc,
		TmpDir:          tmpDir,
		PgContainer:     nil, // No longer managing container per test
		NatsServer:      nil, // No longer managing NATS server per test
		ConnectClient:   connectClient,
		NamespaceClient: namespaceClient,
		TagClient:       tagClient,
		TestServer:      testServer,
	}
}

// Cleanup tears down test resources (but not shared containers)
func (ta *TestApp) Cleanup(t *testing.T) {
	if ta.TestServer != nil {
		ta.TestServer.Close()
	}

	if ta.NC != nil {
		ta.NC.Close()
	}

	if ta.Pool != nil {
		ta.Pool.Close()
	}

	if ta.TmpDir != "" {
		os.RemoveAll(ta.TmpDir)
	}
}

// AssertJSONEqual compares two JSON strings for semantic equality
func AssertJSONEqual(t *testing.T, expected, actual string, msgAndArgs ...interface{}) {
	var expectedJSON, actualJSON interface{}
	require.NoError(t, json.Unmarshal([]byte(expected), &expectedJSON))
	require.NoError(t, json.Unmarshal([]byte(actual), &actualJSON))
	require.Equal(t, expectedJSON, actualJSON, msgAndArgs...)
}

// stringPtr returns a pointer to a string value
func stringPtr(s string) *string {
	return &s
}

// TestHealthEndpoint tests the health check endpoint
func TestHealthEndpoint(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	// Test health endpoint
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	ta.Router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var response struct {
		Status string `json:"status"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	require.Equal(t, "ok", response.Status)

	// Test OpenAPI JSON spec endpoint
	req = httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	w = httptest.NewRecorder()

	ta.Router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "json")

	// Verify it's valid JSON and contains OpenAPI structure
	var spec map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &spec)
	require.NoError(t, err)

	// Check for required OpenAPI fields
	require.Contains(t, spec, "openapi", "Should contain openapi version")
	require.Contains(t, spec, "info", "Should contain info section")
	require.Contains(t, spec, "paths", "Should contain paths section")

	// Verify info section
	info, ok := spec["info"].(map[string]interface{})
	require.True(t, ok, "info should be an object")
	require.Equal(t, "Wayfile Document API", info["title"])
	require.Equal(t, "0.1.0", info["version"])
}
