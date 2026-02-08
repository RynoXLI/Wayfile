# Wayfile AI Coding Instructions

## Architecture Overview
Wayfile is a document management system built with Go, using a multi-layer architecture:
- **API Layer**: Chi router with Huma v2 for HTTP/REST + Connect for gRPC services
- **Service Layer**: Business logic in `internal/services/` (document, namespace, tag services)  
- **Data Layer**: PostgreSQL with SQLC for type-safe SQL + NATS JetStream for events
- **Storage Layer**: Pluggable storage backends (local filesystem, extensible to S3)

## Essential Development Patterns

### Code Generation Stack
This project heavily uses code generation - **always regenerate after schema changes**:
```bash
task protobuf  # Buf protobuf → Connect services
task sqlc      # SQL → Go structs/queries  
task migrate   # Database migrations
```

### Service Architecture Pattern
Services follow dependency injection with clear separation:
- `NewXService()` constructors take all dependencies explicitly
- Services orchestrate across storage, events, and database queries
- Example: `DocumentService` coordinates upload → storage → events → URL signing

### Database Integration
- Use SQLC queries in `internal/db/queries/*.sql` - never write raw SQL in Go code
- All queries return generated types from `internal/db/sqlc/`
- UUIDs are the primary key type throughout
- Namespace-scoped resources (documents, tags) always include namespace_id

### Testing Strategy
- Integration tests use TestContainers with real PostgreSQL + NATS
- **Shared container pattern**: Tests reuse a single PostgreSQL and NATS instance across all tests for performance
- Test pattern: `SetupTestApp()` creates isolated test environment with cleaned database/NATS/storage
- Between tests: Database tables are truncated, NATS streams purged, and storage directories cleaned
- Migrations run once at test suite startup via `TestMain()`
- Run integration tests: `go test -tags=integration ./cmd/api -v`
- Unit tests for services: `go test ./internal/services -v`
- Integration test performance: ~2s for 22 tests (vs ~24s with per-test containers)

### Configuration & Dependencies
- All config in `config.toml` → `internal/config/config.go` structs
- Viper loads config with environment override capability  
- Main dependency setup in `cmd/api/main.go` - follow existing DI patterns

### API & Handler Patterns
**API Method Selection**:
- **New methods should primarily use Connect/gRPC** - define in `proto/*/v1/*.proto`, implement in `cmd/api/rpc/`
- **Async operations use NATS JetStream** - publish events, don't expose as synchronous APIs
- **REST endpoints only for file operations** - document upload/download with multipart forms and pre-signed URLs
- Avoid adding new REST endpoints unless handling file streams

**Implementation Details**:
- Huma v2 for HTTP with OpenAPI generation - use operation structs for input/output
- Connect/gRPC services in `cmd/api/rpc/` implement protobuf-generated interfaces
- Pre-signed URLs for secure document downloads via `internal/auth/presigned.go`
- Rate limiting middleware applied globally

### Event-Driven Architecture  
- Publish domain events via `internal/events/publisher.go` to NATS JetStream
- Event schemas defined in `proto/events/v1/events.proto`
- Async publishing pattern: publish after successful database operations

### Storage Abstraction
- Storage interface in `internal/storage/storage.go` for pluggable backends
- Local filesystem implementation in `internal/storage/local.go`
- Path structure: `{namespace_id}/{document_id}/{filename}`

## Critical File Patterns
- `proto/*/v1/*.proto` → `gen/go/*/v1/` (generated, don't edit)
- `internal/db/queries/*.sql` → `internal/db/sqlc/*.go` (generated)
- Migration files: `migrations/NNN_description.sql` (manual, sequential)
- Integration tests: `cmd/api/*_test.go` with `//go:build integration` tag

## Development Commands
```bash
# Full development setup
task migrate
docker-compose up -d  # Start PostgreSQL + NATS

# Code generation after schema changes  
task protobuf sqlc

# Testing
go test ./internal/services -v                    # Unit tests
go test -tags=integration ./cmd/api -v           # Integration tests
go test -tags=integration ./cmd/api -v -run TestDocumentTag  # Specific test

# Server
task dev  # Uses config.toml, runs on :8080
```

## Key Dependencies & Patterns
- **Database**: `pgx/v5` with connection pooling, SQLC for queries
- **HTTP**: Chi router + Huma v2 decorators, Connect for gRPC-web
- **Events**: NATS JetStream with protobuf serialization
- **Config**: Viper with TOML files and env var overrides
- **Testing**: testify + testcontainers for integration tests
- **Storage**: Interface pattern for multiple backend support

When adding features, follow the established patterns: define protobuf schemas, create SQLC queries, implement in services layer, wire up handlers, add integration tests.