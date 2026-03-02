package anthropic

import (
	"io"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core/sseframe"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

// eventStream implements core.EventStream for Anthropic SSE responses.
type eventStream struct {
	parser *sseframe.Parser
	closer io.Closer
	err    error
}

// newEventStream creates a new event stream from an HTTP response body.
func newEventStream(body io.ReadCloser) *eventStream {
	return &eventStream{
		parser: sseframe.New(body),
		closer: body,
	}
}

// Next returns the next event from the stream.
// Returns nil, io.EOF when the stream is complete.
func (s *eventStream) Next() (types.StreamEvent, error) {
	if s.err != nil {
		return nil, s.err
	}

	for {
		frame, err := s.parser.Next()
		if err != nil {
			if err == io.EOF {
				return nil, io.EOF
			}
			s.err = err
			return nil, err
		}

		eventType := strings.TrimSpace(frame.Event)
		if eventType == "ping" {
			continue
		}

		data := strings.TrimSpace(string(frame.Data))
		if data == "" {
			continue
		}

		if eventType != "" {
			event, err := parseStreamEvent(eventType, []byte(data))
			if err != nil {
				// Log the error but continue processing
				continue
			}

			if event != nil {
				return event, nil
			}
			continue
		}

		// Handle "data:" only format (some SSE implementations).
		event, err := parseStreamEventFromData([]byte(data))
		if err != nil {
			continue // Skip unparseable events
		}
		if event != nil {
			return event, nil
		}
	}
}

// Close releases resources associated with the stream.
func (s *eventStream) Close() error {
	return s.closer.Close()
}

// parseStreamEvent parses a stream event from its type and JSON data.
func parseStreamEvent(eventType string, data []byte) (types.StreamEvent, error) {
	return types.UnmarshalStreamEvent(data)
}

// parseStreamEventFromData parses a stream event from JSON data,
// determining the event type from the "type" field in the data.
func parseStreamEventFromData(data []byte) (types.StreamEvent, error) {
	return types.UnmarshalStreamEvent(data)
}
