package storage

import "errors"

// ErrNotFound is returned when a requested resource does not exist
var ErrNotFound = errors.New("resource not found")

// ErrInvalidNamespace is returned when namespace does not exist
var ErrInvalidNamespace = errors.New("invalid namespace")

// ErrDuplicateFile is returned when a file with the same content (checksum) already exists
var ErrDuplicateFile = errors.New("file with this content already exists")
