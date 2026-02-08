// Package events handles publishing events to NATS JetStream
package events

import (
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/RynoXLI/Wayfile/gen/go/events/v1"
)

// Publisher defines the interface for publishing events
type Publisher interface {
	DocumentUploaded(event *eventsv1.DocumentUploadedEvent) error
	TagSchemaChanged(event *eventsv1.TagSchemaChangedEvent) error
	TagExtracted(event *eventsv1.TagExtractedEvent) error
}

// JetStreamPublisher implements Publisher interface using NATS JetStream
type JetStreamPublisher struct {
	js nats.JetStreamContext
}

// NewPublisher creates a new JetStreamPublisher instance
func NewPublisher(js nats.JetStreamContext) *JetStreamPublisher {
	return &JetStreamPublisher{js: js}
}

func (p *JetStreamPublisher) publish(subject string, event proto.Message) error {
	data, err := proto.Marshal(event)
	if err != nil {
		return err
	}
	_, err = p.js.PublishAsync(subject, data)
	return err
}

// DocumentUploaded publishes a "document.uploaded" event to NATS JetStream
func (p *JetStreamPublisher) DocumentUploaded(event *eventsv1.DocumentUploadedEvent) error {
	return p.publish(DocumentUploaded, event)
}

// TagSchemaChanged publishes a "tags.schema.changed" event to NATS JetStream
func (p *JetStreamPublisher) TagSchemaChanged(event *eventsv1.TagSchemaChangedEvent) error {
	return p.publish(TagSchemaChanged, event)
}

// TagExtracted publishes a "tags.extracted" event to NATS JetStream
func (p *JetStreamPublisher) TagExtracted(event *eventsv1.TagExtractedEvent) error {
	return p.publish(TagExtracted, event)
}
