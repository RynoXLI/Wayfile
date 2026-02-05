// Package storage implements local filesystem storage backend
package storage

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// LocalStorage implements Client interface for local filesystem storage
type LocalStorage struct {
	basePath string
	logger   *slog.Logger
}

// NewLocalStorage creates a new LocalStorage instance
func NewLocalStorage(basePath string, logger *slog.Logger) (*LocalStorage, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, err
	}
	return &LocalStorage{basePath: basePath, logger: logger}, nil
}

// Upload uploads a file to local storage
func (l *LocalStorage) Upload(
	_ context.Context,
	namespaceID string,
	documentID string,
	filename string,
	data io.Reader,
) error {
	fullPath := filepath.Join(l.basePath, namespaceID, documentID, filename)

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}

	file, err := os.Create(fullPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			l.logger.Error("failed to close file", "error", err, "path", fullPath)
		}
	}()
	_, err = io.Copy(file, data)
	return err
}

// Download retrieves a file from local storage
func (l *LocalStorage) Download(
	_ context.Context,
	namespaceID string,
	documentID string,
	filename string,
) (io.ReadCloser, error) {
	fullPath := filepath.Join(l.basePath, namespaceID, documentID, filename)
	file, err := os.Open(fullPath)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return file, err
}

// Delete removes a file from local storage
func (l *LocalStorage) Delete(
	_ context.Context,
	namespaceID string,
	documentID string,
	_ string, // maybe remove filename parameter altogether
) error {
	// Delete the entire document folder
	docPath := filepath.Join(l.basePath, namespaceID, documentID)
	err := os.RemoveAll(docPath)
	if os.IsNotExist(err) {
		return ErrNotFound
	}
	return err
}
