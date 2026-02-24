package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

type Writer struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
}

func New(w http.ResponseWriter) (*Writer, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("response writer does not support flushing")
	}
	return &Writer{w: w, flusher: f}, nil
}

func (sw *Writer) Send(event string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}

	sw.mu.Lock()
	defer sw.mu.Unlock()

	if _, err := fmt.Fprintf(sw.w, "event: %s\n", event); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(sw.w, "data: %s\n\n", string(b)); err != nil {
		return err
	}
	sw.flusher.Flush()
	return nil
}
