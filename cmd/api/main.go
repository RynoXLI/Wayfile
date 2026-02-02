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

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	_ "github.com/RynoXLI/Wayfile/docs"
	"github.com/RynoXLI/Wayfile/internal/config"
	"github.com/RynoXLI/Wayfile/internal/db/sqlc"
	"github.com/RynoXLI/Wayfile/internal/storage"
	"github.com/RynoXLI/Wayfile/pkg/events"
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

	// Connect to PostgreSQL
	pool, err := pgxpool.New(ctx, cfg.Database.URL())
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
	storageService := storage.NewStorage(localClient, queries, publisher, logger)

	// Initialize app
	app := &App{
		storage: storageService,
		logger:  logger,
	}

	// Create Chi router
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

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

	// Start server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	logger.Info("Server listening", "address", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		logger.Error("Server failed", "error", err)
	}
}

type App struct {
	storage *storage.Storage
	logger  *slog.Logger
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
//	@Description	Check if the API server is running
//	@Tags			health
//	@Produce		plain
//	@Success		200	{string}	string	"OK"
//	@Router			/health [get]
func (app *App) healthHandler(w http.ResponseWriter, _ *http.Request) {
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

	// Upload the file using the storage service
	doc, err := app.storage.Upload(ctx, namespace, filename, mimeType, int(size), file)
	if err != nil {
		if errors.Is(err, storage.ErrDuplicateFile) {
			http.Error(w, "File with this content already exists", http.StatusConflict)
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	err = json.NewEncoder(w).Encode(doc)
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
//	@Success		200			{file}		file	"File content"
//	@Failure		404			{string}	string	"File not found"
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

	ctx := r.Context()

	// Download the file from storage
	fileReader, doc, err := app.storage.Download(ctx, namespace, documentID)
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
	err := app.storage.Delete(ctx, namespace, documentID)
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
