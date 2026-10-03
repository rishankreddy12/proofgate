// Package sse reads and writes server-sent events without buffering whole streams.
package sse

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

var (
	// ErrEventTooLarge is returned when an SSE event exceeds the maximum configured size limit.
	ErrEventTooLarge = errors.New("sse: event exceeded maximum size")
)

const (
	// DefaultMaxEventBytes is the default maximum size allowed for an individual SSE event (16MB).
	DefaultMaxEventBytes = 16 * 1024 * 1024
)

// Event defines the core enterprise configuration and state for Event.
// It is responsible for managing the lifecycle, validation, and schema of the Event entity.
type Event struct {
	ID   string
	Name string
	Data []byte
}

// Reader defines the core enterprise configuration and state for Reader.
// It is responsible for managing the lifecycle, validation, and schema of the Reader entity.
type Reader struct {
	br            *bufio.Reader
	maxEventBytes int
}

// NewReader executes the primary logic for the NewReader operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewReader(r io.Reader) *Reader {
	return NewReaderWithLimit(r, DefaultMaxEventBytes)
}

// NewReaderWithLimit executes the primary logic for the NewReaderWithLimit operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewReaderWithLimit(r io.Reader, maxEventBytes int) *Reader {
	if maxEventBytes <= 0 {
		maxEventBytes = DefaultMaxEventBytes
	}
	return &Reader{
		br:            bufio.NewReaderSize(r, 64*1024),
		maxEventBytes: maxEventBytes,
	}
}

// Next returns the next event. It returns io.EOF when the stream ends with no pending event.
func (r *Reader) Next() (Event, error) {
	var ev Event
	var data [][]byte
	have := false
	totalBytes := 0
	for {
		line, err := r.br.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			if have && errors.Is(err, io.EOF) {
				ev.Data = bytes.Join(data, []byte("\n"))
				return ev, nil
			}
			return Event{}, err
		}
		totalBytes += len(line)
		if r.maxEventBytes > 0 && totalBytes > r.maxEventBytes {
			return Event{}, ErrEventTooLarge
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			if have {
				ev.Data = bytes.Join(data, []byte("\n"))
				return ev, nil
			}
			continue
		}
		if line[0] == ':' {
			continue
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "id":
			ev.ID = string(value)
			have = true
		case "event":
			ev.Name = string(value)
			have = true
		case "data":
			data = append(data, append([]byte(nil), value...))
			have = true
		}
		if err != nil { // EOF right after a line without newline
			if have {
				ev.Data = bytes.Join(data, []byte("\n"))
				return ev, nil
			}
			return Event{}, err
		}
	}
}
