// Package services contains business logic and orchestration
package services

import (
	"context"
	"fmt"
	"io"
	"time"

	eventsv1 "github.com/RynoXLI/Wayfile/gen/go/events/v1"
	"github.com/RynoXLI/Wayfile/internal/auth"
	"github.com/RynoXLI/Wayfile/internal/db/sqlc"
	"github.com/RynoXLI/Wayfile/internal/events"
	"github.com/RynoXLI/Wayfile/internal/storage"
)

// DocumentService orchestrates document operations across storage, events, and URL generation
type DocumentService struct {
	storage   *storage.Storage
	publisher events.Publisher
	signer    *auth.Signer
	baseURL   string
}

// NewDocumentService creates a new document service with the given dependencies
func NewDocumentService(
	storage *storage.Storage,
	publisher events.Publisher,
	signer *auth.Signer,
	baseURL string,
) *DocumentService {
	return &DocumentService{
		storage:   storage,
		publisher: publisher,
		signer:    signer,
		baseURL:   baseURL,
	}
}

// DocumentUploadResult contains the uploaded document and its pre-signed download URL
type DocumentUploadResult struct {
	Document    *sqlc.CreateDocumentRow
	DownloadURL string
}

// UploadDocument uploads a document, generates a download URL, and publishes an event
func (s *DocumentService) UploadDocument(
	ctx context.Context,
	namespace string,
	filename string,
	mimeType string,
	fileSize int,
	data io.Reader,
) (*DocumentUploadResult, error) {
	doc, err := s.storage.Upload(ctx, namespace, filename, mimeType, fileSize, data)
	if err != nil {
		return nil, err
	}

	docID := doc.ID.String()
	token := s.signer.GenerateToken(namespace, docID, 24*time.Hour)
	downloadURL := fmt.Sprintf("%s/api/v1/ns/%s/documents/%s?token=%s",
		s.baseURL, namespace, docID, token)

	namespaceID, _ := s.storage.GetNamespaceID(ctx, namespace)
	event := &eventsv1.DocumentUploadedEvent{
		DocumentId:  docID,
		NamespaceId: namespaceID,
		Filename:    filename,
		MimeType:    mimeType,
	}
	if err := s.publisher.DocumentUploaded(event); err != nil {
		return nil, err
	}

	return &DocumentUploadResult{
		Document:    doc,
		DownloadURL: downloadURL,
	}, nil
}

// DownloadDocument retrieves a document from storage
func (s *DocumentService) DownloadDocument(
	ctx context.Context,
	namespace string,
	documentID string,
) (io.ReadCloser, *sqlc.Document, error) {
	return s.storage.Download(ctx, namespace, documentID)
}

// DeleteDocument removes a document from storage
func (s *DocumentService) DeleteDocument(
	ctx context.Context,
	namespace string,
	documentID string,
) error {
	return s.storage.Delete(ctx, namespace, documentID)
}

// GenerateDownloadURL creates a pre-signed download URL for a document
func (s *DocumentService) GenerateDownloadURL(
	namespace string,
	documentID string,
	ttl time.Duration,
) string {
	token := s.signer.GenerateToken(namespace, documentID, ttl)
	return fmt.Sprintf("%s/api/v1/ns/%s/documents/%s?token=%s",
		s.baseURL, namespace, documentID, token)
}
