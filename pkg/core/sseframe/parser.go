package sseframe

import (
	"bufio"
	"io"
	"strings"
)

// Frame is one parsed SSE frame.
type Frame struct {
	Event string
	Data  []byte
}

// Parser reads SSE frames from a stream.
type Parser struct {
	reader *bufio.Reader
}

// New creates a parser from an input stream.
func New(r io.Reader) *Parser {
	return &Parser{reader: bufio.NewReader(r)}
}

// Next returns the next SSE frame.
// It joins multiline data fields using "\n" and returns a partial frame on EOF.
func (p *Parser) Next() (Frame, error) {
	var frame Frame
	var dataLines []string

	for {
		line, err := p.reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return Frame{}, err
		}

		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")

		if line == "" {
			if hasFrameContent(frame, dataLines) {
				frame.Data = []byte(strings.Join(dataLines, "\n"))
				return frame, nil
			}
			if err == io.EOF {
				return Frame{}, io.EOF
			}
			continue
		}

		if strings.HasPrefix(line, ":") {
			if err == io.EOF {
				if hasFrameContent(frame, dataLines) {
					frame.Data = []byte(strings.Join(dataLines, "\n"))
					return frame, nil
				}
				return Frame{}, io.EOF
			}
			continue
		}

		field, value := splitField(line)
		switch field {
		case "event":
			frame.Event = value
		case "data":
			dataLines = append(dataLines, value)
		default:
			// Ignore unknown/unsupported fields.
		}

		if err == io.EOF {
			if hasFrameContent(frame, dataLines) {
				frame.Data = []byte(strings.Join(dataLines, "\n"))
				return frame, nil
			}
			return Frame{}, io.EOF
		}
	}
}

func splitField(line string) (string, string) {
	field, value, found := strings.Cut(line, ":")
	if !found {
		return line, ""
	}
	if strings.HasPrefix(value, " ") {
		value = value[1:]
	}
	return field, value
}

func hasFrameContent(frame Frame, dataLines []string) bool {
	return frame.Event != "" || len(dataLines) > 0
}
