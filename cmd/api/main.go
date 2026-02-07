// cmd/api/main.go API server entry point
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/RynoXLI/Wayfile/cmd/api/rpc"
	documentsv1 "github.com/RynoXLI/Wayfile/gen/go/documents/v1/documentsv1connect"
	namespacesv1 "github.com/RynoXLI/Wayfile/gen/go/namespaces/v1/namespacesv1connect"
	"github.com/RynoXLI/Wayfile/gen/go/tags/v1/tagsv1connect"
	"github.com/RynoXLI/Wayfile/internal/auth"
	"github.com/RynoXLI/Wayfile/internal/config"
	"github.com/RynoXLI/Wayfile/internal/db/sqlc"
	"github.com/RynoXLI/Wayfile/internal/events"
	"github.com/RynoXLI/Wayfile/internal/middleware"
	"github.com/RynoXLI/Wayfile/internal/services"
	"github.com/RynoXLI/Wayfile/internal/storage"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("Failed to load config:", err)
	}

	// Setup logger
	var handler slog.Handler
	if cfg.Logging.Format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, nil)
	} else {
		handler = slog.NewTextHandler(os.Stdout, nil)
	}
	logger := slog.New(handler)
	logger.Info("Starting API server...")

	ctx := context.Background()

	// Parse connection string from config into pool config
	poolConfig, err := pgxpool.ParseConfig(cfg.Database.URL)
	if err != nil {
		log.Fatal("Unable to parse database config:", err)
	}

	// Connect to PostgreSQL
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		log.Fatal("Unable to connect to database:", err)
	}
	defer pool.Close()

	logger.Info("Connected to PostgreSQL")

	// Connect to NATS and create JetStream context
	nc, err := nats.Connect(cfg.NATS.URL)
	if err != nil {
		log.Fatal("Unable to connect to NATS:", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		log.Fatal("Unable to create JetStream context:", err)
	}

	logger.Info("Connected to NATS with JetStream")

	// Initialize storage client
	localClient, err := storage.NewLocalStorage(cfg.Storage.Local.Path, logger)
	if err != nil {
		log.Fatal("Unable to initialize storage:", err)
	}

	if cfg.Storage.Type != "local" {
		log.Fatal("Unsupported storage type:", cfg.Storage.Type)
	}
	logger.Info("Storage initialized", "type", cfg.Storage.Type, "path", cfg.Storage.Local.Path)

	// Initialize event publisher and storage
	publisher := events.NewPublisher(js)
	queries := sqlc.New(pool)
	storageService := storage.NewStorage(localClient, queries, logger)

	// Initialize document service
	signer := auth.NewSigner(cfg.Server.SigningSecret)
	documentService := services.NewDocumentService(
		storageService,
		publisher,
		signer,
		cfg.Server.BaseURL,
		queries,
	)

	// Initialize namespace service
	namespaceService := services.NewNamespaceService(queries)

	// Initialize tag service
	tagService := services.NewTagService(queries, publisher)

	// Initialize app
	app := &App{
		DocumentService: documentService,
		Logger:          logger,
		Signer:          signer,
		BaseURL:         cfg.Server.BaseURL,
		Pool:            pool,
		NC:              nc,
	}

	// Setup router with Huma
	router := chi.NewRouter()

	// Middleware
	router.Use(chimiddleware.RequestID)
	router.Use(chimiddleware.Logger)
	router.Use(chimiddleware.Recoverer)
	router.Use(chimiddleware.SetHeader("X-Content-Type-Options", "nosniff"))
	router.Use(middleware.RateLimiter(cfg.Server.RateLimitRPS, cfg.Server.RateLimitBurst))

	// Apply max upload size to POST routes only
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				r.Body = http.MaxBytesReader(w, r.Body, cfg.Server.MaxUploadSize)
			}
			next.ServeHTTP(w, r)
		})
	})

	// Create Huma API with chi adapter
	humaConfig := huma.DefaultConfig("Wayfile Document API", "0.1.0")
	humaConfig.Info.Description = "Document storage and management API"
	humaConfig.Info.License = &huma.License{
		Name: "Apache 2.0",
		URL:  "http://www.apache.org/licenses/LICENSE-2.0.html",
	}
	humaConfig.Servers = []*huma.Server{
		{URL: cfg.Server.BaseURL},
	}
	humaConfig.OpenAPIPath = "/openapi.json"
	if !cfg.Server.EnableDocs {
		humaConfig.DocsPath = "" // Disable /docs endpoint
	}

	api := humachi.New(router, humaConfig)

	// Register all routes
	RegisterRoutes(api, app)

	// Mount Connect RPC handlers
	documentsRPCService := rpc.NewDocumentsServiceServer(documentService)
	connectPath, connectHandler := documentsv1.NewDocumentServiceHandler(
		documentsRPCService,
		connect.WithInterceptors(),
	)
	router.Mount(connectPath, connectHandler)

	// Mount Namespace RPC handlers
	namespaceRPCService := rpc.NewNamespaceServiceServer(namespaceService)
	namespacePath, namespaceHandler := namespacesv1.NewNamespaceServiceHandler(
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

	// Add endpoint for OpenAPI 3.0.3 (downgraded for oapi-codegen)
	router.Get("/openapi-3.0.yaml", func(w http.ResponseWriter, _ *http.Request) {
		b, err := api.OpenAPI().DowngradeYAML()
		if err != nil {
			logger.Error("failed to downgrade OpenAPI spec", "error", err)
			http.Error(
				w,
				http.StatusText(http.StatusInternalServerError),
				http.StatusInternalServerError,
			)
			return
		}
		w.Header().Set("Content-Type", "application/x-yaml")
		if _, err := w.Write(b); err != nil {
			logger.Error("failed to write OpenAPI spec response", "error", err)
		}
	})

	// Use h2c for HTTP/2 without TLS (required for Connect RPC)
	h2cHandler := h2c.NewHandler(router, &http2.Server{})

	// Start server with timeouts
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      h2cHandler,
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(cfg.Server.IdleTimeout) * time.Second,
	}

	logger.Info("Server listening", "address", addr)
	if cfg.Server.EnableDocs {
		logger.Info("OpenAPI docs available at", "url", cfg.Server.BaseURL+"/docs")
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal("Server failed:", err)
	}
}

type App struct {
	DocumentService *services.DocumentService
	Logger          *slog.Logger
	Signer          *auth.Signer
	BaseURL         string
	Pool            *pgxpool.Pool
	NC              *nats.Conn
}
