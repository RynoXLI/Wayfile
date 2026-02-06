package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RynoXLI/Wayfile/internal/db/sqlc"
)

// Client defines the interface for storage backends
type Client interface {
	Upload(
		ctx context.Context,
		namespaceID string,
		documentID string,
		filename string,
		data io.Reader,
	) error
	Download(
		ctx context.Context,
		namespaceID string,
		documentID string,
		filename string,
	) (io.ReadCloser, error)
	Delete(ctx context.Context, namespaceID string, documentID string, filename string) error
}

// Storage is the backend for managing document storage
type Storage struct {
	client  Client
	queries *sqlc.Queries
	logger  *slog.Logger
}

// NewStorage creates a new Storage instance
func NewStorage(
	client Client,
	queries *sqlc.Queries,
	logger *slog.Logger,
) *Storage {
	return &Storage{
		client:  client,
		queries: queries,
		logger:  logger,
	}
}

// validateDocument checks if document exists and namespace matches
func (s *Storage) validateDocument(
	ctx context.Context,
	namespace, documentID string,
) (*sqlc.Document, error) {
	var pgDocID pgtype.UUID
	if err := pgDocID.Scan(documentID); err != nil {
		return nil, err
	}

	doc, err := s.queries.GetDocumentByID(ctx, pgDocID)
	if err != nil {
		return nil, ErrNotFound
	}

	// Get namespace by name to get its UUID
	ns, err := s.queries.GetNamespaceByName(ctx, namespace)
	if err != nil {
		return nil, ErrNotFound
	}

	// Compare namespace UUIDs
	if doc.NamespaceID.Bytes != ns.ID.Bytes {
		return nil, ErrNotFound
	}

	return &doc, nil
}

// Upload uploads a document to storage, records its metadata in the database, and emits an event
func (s *Storage) Upload(ctx context.Context,
	namespace string,
	filename string,
	mimeType string,
	fileSize int,
	data io.Reader) (*sqlc.CreateDocumentRow, error) {
	docID := uuid.New()

	// Calculate checksum while uploading using TeeReader
	hash := sha256.New()
	teeReader := io.TeeReader(data, hash)

	ns, err := s.queries.GetNamespaceByName(ctx, namespace)
	if err != nil {
		return nil, ErrNotFound
	}

	namespaceUUID, err := uuid.Parse(ns.ID.String())
	if err != nil {
		return nil, err
	}

	err = s.client.Upload(ctx, namespaceUUID.String(), docID.String(), filename, teeReader)
	if err != nil {
		return nil, err
	}

	// Defer cleanup in case database operations fail
	var shouldCleanup bool
	defer func() {
		if shouldCleanup {
			if delErr := s.client.Delete(
				ctx,
				namespaceUUID.String(),
				docID.String(),
				filename,
			); delErr != nil {
				s.logger.Error("Failed to cleanup uploaded file after error",
					"document_id", docID.String(),
					"namespace_id", namespaceUUID.String(),
					"filename", filename,
					"error", delErr,
				)
			}
		}
	}()

	checkSum := hex.EncodeToString(hash.Sum(nil))

	// Convert UUIDs to pgtype.UUID
	var pgDocID pgtype.UUID
	_ = pgDocID.Scan(docID.String())

	// Submit metadata to postgres
	doc, err := s.queries.CreateDocument(ctx,
		pgDocID,
		ns.ID,           // namespace_id
		filename,        // file_name
		filename,        // title
		mimeType,        // mime_type
		checkSum,        // checksum_sha256
		int64(fileSize), // file_size
	)
	if err != nil {
		shouldCleanup = true
		// Check for unique constraint violation on checksum
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// 23505 is unique_violation
			return nil, ErrDuplicateFile
		}
		return nil, err
	}

	return &doc, nil
}

// GetNamespaceID retrieves the namespace UUID by name
func (s *Storage) GetNamespaceID(ctx context.Context, namespace string) (string, error) {
	ns, err := s.queries.GetNamespaceByName(ctx, namespace)
	if err != nil {
		return "", ErrNotFound
	}
	return ns.ID.String(), nil
}

// Download retrieves a document from storage
func (s *Storage) Download(ctx context.Context,
	namespace string,
	documentID string) (io.ReadCloser, *sqlc.Document, error) {
	doc, err := s.validateDocument(ctx, namespace, documentID)
	if err != nil {
		return nil, nil, err
	}

	namespaceUUID, _ := uuid.Parse(doc.NamespaceID.String())
	fileReader, err := s.client.Download(ctx, namespaceUUID.String(), documentID, doc.FileName)
	if err != nil {
		return nil, nil, err
	}
	return fileReader, doc, nil
}

// Delete removes a document from storage
func (s *Storage) Delete(ctx context.Context,
	namespace string,
	documentID string) error {
	doc, err := s.validateDocument(ctx, namespace, documentID)
	if err != nil {
		return err
	}

	namespaceUUID, _ := uuid.Parse(doc.NamespaceID.String())
	err = s.client.Delete(ctx, namespaceUUID.String(), documentID, doc.FileName)
	if err != nil {
		return err
	}
	var pgDocID pgtype.UUID
	_ = pgDocID.Scan(documentID)
	err = s.queries.DeleteDocument(ctx, pgDocID)
	if err != nil {
		return err
	}
	return nil
}
