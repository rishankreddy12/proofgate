// Package sse reads and writes server-sent events without buffering whole streams.
package sse

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

type Event struct {
	ID   string
	Name string
	Data []byte
}

type Reader struct {
	br *bufio.Reader
}

func NewReader(r io.Reader) *Reader {
	return &Reader{br: bufio.NewReaderSize(r, 64*1024)}
}

// Next returns the next event. It returns io.EOF when the stream ends with no pending event.
func (r *Reader) Next() (Event, error) {
	var ev Event
	var data [][]byte
	have := false
	for {
		line, err := r.br.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			if have && errors.Is(err, io.EOF) {
				ev.Data = bytes.Join(data, []byte("\n"))
				return ev, nil
			}
			return Event{}, err
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
