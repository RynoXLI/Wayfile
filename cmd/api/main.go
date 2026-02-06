// cmd/api/main.go API server entry point
//
//	@title						Wayfile Document API
//	@version					0.1.0
//	@description				Document storage and management API
//
//	@license.name				Apache 2.0
//	@license.url				http://www.apache.org/licenses/LICENSE-2.0.html
//
//	@host						localhost:8080
//	@BasePath					/api/v1
//
//	@schemes					http https
//
//	@tag.name					documents
//	@tag.description			Document storage operations
//
//	@tag.name					health
//	@tag.description			Health check endpoints
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	_ "github.com/RynoXLI/Wayfile/docs"
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
	)

	// Initialize app
	app := &App{
		documentService: documentService,
		logger:          logger,
		signer:          signer,
		baseURL:         cfg.Server.BaseURL,
		pool:            pool,
		nc:              nc,
	}

	// Setup router
	r := ChiRouter(app, cfg)

	// Start server with timeouts
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(cfg.Server.IdleTimeout) * time.Second,
	}

	logger.Info("Server listening", "address", addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal("Server failed:", err)
	}
}

func ChiRouter(app *App, cfg *config.Config) chi.Router {
	// Create Chi router
	r := chi.NewRouter()

	// Middleware
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.SetHeader("X-Content-Type-Options", "nosniff"))
	r.Use(middleware.RateLimiter(cfg.Server.RateLimitRPS, cfg.Server.RateLimitBurst))

	// Apply max upload size to POST routes only
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				r.Body = http.MaxBytesReader(w, r.Body, cfg.Server.MaxUploadSize)
			}
			next.ServeHTTP(w, r)
		})
	})

	// Register routes under /api/v1
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", app.healthHandler)

		r.Route("/ns/{namespace}", func(r chi.Router) {
			r.Post("/documents", app.uploadHandler)
			r.Get("/documents/{documentID}", app.downloadHandler)
			r.Delete("/documents/{documentID}", app.deleteHandler)
		})
	})

	// Swagger UI
	r.Get("/swagger/*", httpSwagger.WrapHandler)

	return r
}

type App struct {
	documentService *services.DocumentService
	logger          *slog.Logger
	signer          *auth.Signer
	baseURL         string
	pool            *pgxpool.Pool
	nc              *nats.Conn
}

// validateUUID checks if a string is a valid UUID and writes 404 if not
func (app *App) validateUUID(w http.ResponseWriter, id string) bool {
	if _, err := uuid.Parse(id); err != nil {
		http.Error(w, "Invalid document ID", http.StatusNotFound)
		return false
	}
	return true
}

// healthHandler godoc
//
//	@Summary		Health check
//	@Description	Check if the API server is running and dependencies are healthy
//	@Tags			health
//	@Produce		plain
//	@Success		200	{string}	string	"OK"
//	@Failure		503	{string}	string	"Service Unavailable"
//	@Router			/health [get]
func (app *App) healthHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	// Check database
	if err := app.pool.Ping(ctx); err != nil {
		app.logger.Error("Health check failed: database", "error", err)
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return
	}

	// Check NATS
	if !app.nc.IsConnected() {
		app.logger.Error("Health check failed: NATS disconnected")
		http.Error(w, "Message queue unavailable", http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, err := w.Write([]byte("OK"))
	if err != nil {
		app.logger.Error("Failed to write health response", "error", err)
	}
}

// uploadHandler godoc
//
//	@Summary		Upload a document
//	@Description	Upload a file to the specified namespace
//	@Tags			documents
//	@Accept			multipart/form-data
//	@Produce		json
//	@Param			namespace	path		string	true	"Namespace name"
//	@Param			file		formData	file	true	"File to upload"
//	@Success		201			{object}	DocumentResponse
//	@Failure		400			{object}	ErrorResponse
//	@Failure		409			{object}	ErrorResponse	"File with this content already exists"
//	@Failure		500			{object}	ErrorResponse
//	@Router			/ns/{namespace}/documents [post]
func (app *App) uploadHandler(w http.ResponseWriter, r *http.Request) {
	// Handle file upload requests
	err := r.ParseMultipartForm(10 << 20) // 10 MB
	if err != nil {
		app.logger.Error("Failed to parse form", "error", err)
		http.Error(w, "Unable to parse form", http.StatusBadRequest)
		return
	}

	// Get the file from the request
	file, handler, err := r.FormFile("file")
	if err != nil {
		app.logger.Error("Failed to get file from form", "error", err)
		http.Error(w, "Error retrieving the file", http.StatusBadRequest)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			app.logger.Error("Failed to close file", "error", err)
		}
	}()

	filename := handler.Filename
	size := handler.Size
	mimeType := handler.Header.Get("Content-Type")
	namespace := chi.URLParam(r, "namespace")
	ctx := r.Context()

	// Upload the file using the document service
	result, err := app.documentService.UploadDocument(
		ctx,
		namespace,
		filename,
		mimeType,
		int(size),
		file,
	)
	if err != nil {
		if errors.Is(err, storage.ErrDuplicateFile) {
			http.Error(w, "File with this content already exists", http.StatusConflict)
			return
		}
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, "Namespace not found", http.StatusNotFound)
			return
		}
		app.logger.Error(
			"Failed to upload file",
			"error",
			err,
			"namespace",
			namespace,
			"filename",
			filename,
		)
		http.Error(w, "Error uploading the file", http.StatusInternalServerError)
		return
	}

	// Create response with download URL
	response := DocumentResponse{
		ID:          result.Document.ID.String(),
		FileName:    result.Document.FileName,
		Title:       result.Document.Title,
		ChecksumSHA: result.Document.ChecksumSha256,
		DownloadURL: result.DownloadURL,
		CreatedAt:   result.Document.CreatedAt.Time,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		app.logger.Error("Failed to encode response", "error", err)
	}
}

// downloadHandler godoc
//
//	@Summary		Download a document
//	@Description	Download a file from the specified namespace
//	@Tags			documents
//	@Produce		octet-stream
//	@Param			namespace	path		string	true	"Namespace name"
//	@Param			documentID	path		string	true	"Document UUID"
//	@Param			token		query		string	false	"Pre-signed token for authentication"
//	@Param			expires		query		string	false	"Token expiration timestamp"
//	@Success		200			{file}		file	"File content"
//	@Failure		404			{string}	string	"File not found"
//	@Failure		401			{string}	string	"Invalid or expired token"
//	@Failure		500			{string}	string	"Internal server error"
//	@Router			/ns/{namespace}/documents/{documentID} [get]
func (app *App) downloadHandler(w http.ResponseWriter, r *http.Request) {
	// Get parameters from path
	namespace := chi.URLParam(r, "namespace")
	documentID := chi.URLParam(r, "documentID")

	// Validate UUID format
	if !app.validateUUID(w, documentID) {
		return
	}

	// Check for pre-signed token
	token := r.URL.Query().Get("token")
	if token != "" {
		// Verify the token
		tokenNs, tokenDocID, err := app.signer.VerifyToken(token)
		if err != nil {
			if errors.Is(err, auth.ErrTokenExpired) {
				http.Error(w, "Token expired", http.StatusUnauthorized)
				return
			}
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}

		// Ensure token matches requested resource
		if tokenNs != namespace || tokenDocID != documentID {
			http.Error(w, "Token does not match requested resource", http.StatusUnauthorized)
			return
		}
	}
	// If no token, this is a regular authenticated request
	// Add your auth middleware here when needed

	ctx := r.Context()

	// Download the file from storage
	fileReader, doc, err := app.documentService.DownloadDocument(ctx, namespace, documentID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Error downloading file", http.StatusInternalServerError)
		return
	}
	defer func() {
		if err := fileReader.Close(); err != nil {
			app.logger.Error("Failed to close file reader", "error", err)
		}
	}()

	// Set headers for file download
	w.Header().Set("Content-Type", doc.MimeType)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+doc.FileName+"\"")

	// Stream file to response
	_, err = io.Copy(w, fileReader)
	if err != nil {
		app.logger.Error("Error streaming file", "error", err)
	}
}

// deleteHandler godoc
//
//	@Summary		Delete a document
//	@Description	Delete a file from the specified namespace
//	@Tags			documents
//	@Produce		json
//	@Param			namespace	path		string	true	"Namespace name"
//	@Param			documentID	path		string	true	"Document UUID"
//	@Success		204			{string}	string	"No content"
//	@Failure		404			{string}	string	"File not found"
//	@Failure		500			{string}	string	"Internal server error"
//	@Router			/ns/{namespace}/documents/{documentID} [delete]
func (app *App) deleteHandler(w http.ResponseWriter, r *http.Request) {
	// Get parameters from path
	namespace := chi.URLParam(r, "namespace")
	documentID := chi.URLParam(r, "documentID")

	// Validate UUID format
	if !app.validateUUID(w, documentID) {
		return
	}

	ctx := r.Context()

	// Delete the file from storage
	err := app.documentService.DeleteDocument(ctx, namespace, documentID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Error deleting file", http.StatusInternalServerError)
		return
	}

	// Return success
	w.WriteHeader(http.StatusNoContent)
}
