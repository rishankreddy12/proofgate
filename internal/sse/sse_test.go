package sse

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReaderParsesEvents(t *testing.T) {
	in := ": comment\n\nevent: message_start\ndata: {\"a\":1}\n\ndata: line1\ndata: line2\n\ndata: [DONE]\n\n"
	r := NewReader(strings.NewReader(in))

	e, err := r.Next()
	require.NoError(t, err)
	require.Equal(t, "message_start", e.Name)
	require.Equal(t, `{"a":1}`, string(e.Data))

	e, err = r.Next()
	require.NoError(t, err)
	require.Equal(t, "line1\nline2", string(e.Data))

	e, err = r.Next()
	require.NoError(t, err)
	require.Equal(t, "[DONE]", string(e.Data))

	_, err = r.Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestReaderHandlesCRLFAndNoTrailingBlank(t *testing.T) {
	r := NewReader(strings.NewReader("data: x\r\n\r\ndata: y"))
	e, _ := r.Next()
	require.Equal(t, "x", string(e.Data))
	e, err := r.Next()
	require.NoError(t, err)
	require.Equal(t, "y", string(e.Data))
}

func TestWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	w, err := NewWriter(rec)
	require.NoError(t, err)
	require.False(t, w.Started())
	require.NoError(t, w.Data(map[string]int{"n": 1}))
	require.True(t, w.Started())
	require.NoError(t, w.Done())
	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	require.Equal(t, "data: {\"n\":1}\n\ndata: [DONE]\n\n", rec.Body.String())
	require.True(t, rec.Flushed)
}

func TestEventIDAndRawWrite(t *testing.T) {
	r := NewReader(strings.NewReader("id: 42\nevent: message\ndata: {}\n\n"))
	e, err := r.Next()
	require.NoError(t, err)
	require.Equal(t, "42", e.ID)
	rec := httptest.NewRecorder()
	w, _ := NewWriter(rec)
	require.NoError(t, w.Event(e))
	require.Equal(t, "id: 42\nevent: message\ndata: {}\n\n", rec.Body.String())
}

