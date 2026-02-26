package vai

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

type sseFrame struct {
	Event string
	Data  []byte
}

type sseParser struct {
	reader *bufio.Reader
}

func newSSEParser(r io.Reader) *sseParser {
	return &sseParser{reader: bufio.NewReader(r)}
}

func (p *sseParser) Next() (sseFrame, error) {
	var eventType string
	var dataLines []string

	for {
		line, err := p.reader.ReadString('\n')
		eof := errors.Is(err, io.EOF)
		if err != nil && !eof {
			return sseFrame{}, err
		}

		if line != "" {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
		}

		if line == "" {
			if len(dataLines) == 0 && eventType == "" {
				if eof {
					return sseFrame{}, io.EOF
				}
				continue
			}
			return sseFrame{
				Event: eventType,
				Data:  []byte(strings.Join(dataLines, "\n")),
			}, nil
		}

		if strings.HasPrefix(line, ":") {
			if eof {
				if len(dataLines) == 0 && eventType == "" {
					return sseFrame{}, io.EOF
				}
				return sseFrame{
					Event: eventType,
					Data:  []byte(strings.Join(dataLines, "\n")),
				}, nil
			}
			continue
		}

		field, value := splitSSEField(line)
		switch field {
		case "event":
			eventType = value
		case "data":
			dataLines = append(dataLines, value)
		}

		if eof {
			if len(dataLines) == 0 && eventType == "" {
				return sseFrame{}, io.EOF
			}
			return sseFrame{
				Event: eventType,
				Data:  []byte(strings.Join(dataLines, "\n")),
			}, nil
		}
	}
}

func splitSSEField(line string) (field string, value string) {
	index := strings.IndexByte(line, ':')
	if index < 0 {
		return line, ""
	}
	field = line[:index]
	value = line[index+1:]
	if strings.HasPrefix(value, " ") {
		value = value[1:]
	}
	return field, value
}

type gatewayMessageEventStream struct {
	body      io.ReadCloser
	parser    *sseParser
	endpoint  string
	closed    atomic.Bool
	closeOnce sync.Once
}

func newGatewayMessageEventStream(body io.ReadCloser, endpoint string) core.EventStream {
	return &gatewayMessageEventStream{
		body:     body,
		parser:   newSSEParser(body),
		endpoint: endpoint,
	}
}

func (s *gatewayMessageEventStream) Next() (types.StreamEvent, error) {
	for {
		frame, err := s.parser.Next()
		if err != nil {
			if errors.Is(err, io.EOF) || s.closed.Load() {
				return nil, io.EOF
			}
			return nil, &TransportError{
				Op:  "POST",
				URL: s.endpoint,
				Err: err,
			}
		}

		if len(frame.Data) == 0 {
			continue
		}

		event, err := types.UnmarshalStreamEvent(frame.Data)
		if err != nil {
			return nil, core.NewAPIError("failed to decode stream event")
		}

		switch e := event.(type) {
		case types.UnknownStreamEvent:
			continue
		case types.ErrorEvent:
			return e, typesErrorToCoreError(e.Error)
		default:
			return event, nil
		}
	}
}

func (s *gatewayMessageEventStream) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		if s.body != nil {
			closeErr = s.body.Close()
		}
	})
	return closeErr
}
