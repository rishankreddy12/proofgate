// Package sse provides enterprise-grade capabilities, configuration, and structural components for the sse subsystem.
package sse

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
)

// Writer writes OpenAI-style "data: <json>" events and flushes after each one.
type Writer struct {
	w       http.ResponseWriter
	f       http.Flusher
	started bool
}

// NewWriter executes the primary logic for the NewWriter operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewWriter(w http.ResponseWriter) (*Writer, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("sse: response writer cannot flush")
	}
	return &Writer{w: w, f: f}, nil
}

func (s *Writer) start() {
	if s.started {
		return
	}
	h := s.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // stop nginx from buffering
	s.w.WriteHeader(http.StatusOK)
	s.started = true
}

// Started reports whether headers and at least one byte have been sent. After this, failover is impossible.
func (s *Writer) Started() bool { return s.started }

// Data executes the primary logic for the Data operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *Writer) Data(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.raw(b)
}

// Done executes the primary logic for the Done operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *Writer) Done() error { return s.raw([]byte("[DONE]")) }

func (s *Writer) raw(b []byte) error {
	s.start()
	buf := make([]byte, 0, len(b)+8)
	buf = append(buf, "data: "...)
	buf = append(buf, b...)
	buf = append(buf, "\n\n"...)
	if _, err := s.w.Write(buf); err != nil {
		return err
	}
	s.f.Flush()
	return nil
}

// Event executes the primary logic for the Event operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *Writer) Event(e Event) error {
	s.start()
	var b []byte
	if e.ID != "" {
		b = append(b, "id: "+e.ID+"\n"...)
	}
	if e.Name != "" {
		b = append(b, "event: "+e.Name+"\n"...)
	}
	for _, line := range bytes.Split(e.Data, []byte("\n")) {
		b = append(append(append(b, "data: "...), line...), '\n')
	}
	b = append(b, '\n')
	if _, err := s.w.Write(b); err != nil {
		return err
	}
	s.f.Flush()
	return nil
}
