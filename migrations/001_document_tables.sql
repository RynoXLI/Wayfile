-- Write your migrate up statements here

CREATE TABLE namespaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    modified_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    namespace_id UUID NOT NULL REFERENCES namespaces(id) ON DELETE CASCADE,

    -- File metadata
    file_name VARCHAR(255) NOT NULL,
    title VARCHAR(255) NOT NULL,
    document_date DATE,
    mime_type VARCHAR(100) NOT NULL,
    checksum_sha256 CHAR(64) NOT NULL,
    file_size BIGINT NOT NULL,
    page_count INT,
    attributes JSONB, -- global attributes
    attributes_version BIGINT, -- version of the attributes schema
    attributes_metadata JSONB DEFAULT '{}'::jsonb, -- provenance metadata for attributes

    -- Record metadata
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    modified_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(namespace_id, checksum_sha256)
);

CREATE TABLE tags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    namespace_id UUID NOT NULL REFERENCES namespaces(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    description TEXT,
    path VARCHAR(255) NOT NULL, -- '/financial/reports/2023'
    parent_id UUID REFERENCES tags(id) ON DELETE CASCADE,  -- NULL for root tags
    color VARCHAR(7),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    modified_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    
    UNIQUE(namespace_id, name)
);

CREATE TABLE document_tags (
    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    tag_id UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    attributes JSONB, -- tag specific attributes
    attributes_version BIGINT, -- version of the attributes schema
    attributes_metadata JSONB DEFAULT '{}'::jsonb, -- provenance metadata for attributes
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    modified_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (document_id, tag_id)
);

CREATE TABLE attribute_schemas (
    tag_id UUID REFERENCES tags(id) ON DELETE CASCADE, -- NULL for global attributes
    version BIGINT NOT NULL,
    json_schema JSONB NOT NULL, -- JSON Schema definition
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tag_id, version)
);

-- Auto-increment version per tag_id
CREATE OR REPLACE FUNCTION set_attribute_schema_version()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.version IS NULL THEN
        SELECT COALESCE(MAX(version), 0) + 1
        INTO NEW.version
        FROM attribute_schemas
        WHERE tag_id = NEW.tag_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_set_attribute_schema_version
BEFORE INSERT ON attribute_schemas
FOR EACH ROW
EXECUTE FUNCTION set_attribute_schema_version();

-- Auto-set attributes_version for documents (global schema)
CREATE OR REPLACE FUNCTION set_document_attributes_version()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.attributes_version IS NULL AND NEW.attributes IS NOT NULL THEN
        SELECT MAX(version)
        INTO NEW.attributes_version
        FROM attribute_schemas
        WHERE tag_id IS NULL;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_set_document_attributes_version
BEFORE INSERT OR UPDATE ON documents
FOR EACH ROW
EXECUTE FUNCTION set_document_attributes_version();

-- Auto-set attributes_version for document_tags (tag-specific schema)
CREATE OR REPLACE FUNCTION set_document_tag_attributes_version()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.attributes_version IS NULL AND NEW.attributes IS NOT NULL THEN
        SELECT MAX(version)
        INTO NEW.attributes_version
        FROM attribute_schemas
        WHERE tag_id = NEW.tag_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_set_document_tag_attributes_version
BEFORE INSERT OR UPDATE ON document_tags
FOR EACH ROW
EXECUTE FUNCTION set_document_tag_attributes_version();

-- Documents
CREATE INDEX idx_documents_namespace_id ON documents(namespace_id);
CREATE INDEX idx_documents_document_date ON documents(document_date);
CREATE INDEX idx_documents_checksum ON documents(checksum_sha256);
CREATE INDEX idx_documents_created_at ON documents(created_at);
CREATE INDEX idx_documents_attributes ON documents USING GIN (attributes);

-- Tags  
CREATE INDEX idx_tags_parent_id ON tags(parent_id);
CREATE INDEX idx_tags_path ON tags(path);

-- Document_tags (reverse lookup)
CREATE INDEX idx_document_tags_tag_id ON document_tags(tag_id);
CREATE INDEX idx_document_tags_attributes ON document_tags USING GIN (attributes);

INSERT INTO namespaces (name) VALUES ('default');

---- create above / drop below ----

DROP TABLE IF EXISTS attribute_schemas;
DROP TABLE IF EXISTS document_tags;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS documents;
DROP TABLE IF EXISTS namespaces;
