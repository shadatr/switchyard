// Package sse reads and writes Server-Sent Events as used by OpenAI-style
// streaming APIs.
package sse

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
)

// MaxEventBytes bounds the size of a single event to protect memory.
const MaxEventBytes = 1 << 20

// ErrEventTooLarge is returned when an event exceeds MaxEventBytes.
var ErrEventTooLarge = errors.New("sse: event too large")

// Done is the OpenAI end-of-stream sentinel payload.
var Done = []byte("[DONE]")

// Event is one dispatched SSE event.
type Event struct {
	Name string // "event:" field; empty means "message"
	Data []byte // "data:" lines joined with "\n"
}

// IsDone reports whether the event is the OpenAI [DONE] sentinel.
func (e Event) IsDone() bool { return bytes.Equal(e.Data, Done) }

// Reader parses an SSE stream.
type Reader struct {
	r         *bufio.Reader
	data      bytes.Buffer
	dataLines int // data lines in the current event; lines are joined with "\n"
	name      string
	has       bool // an event field has been seen since the last dispatch
}

// NewReader returns a Reader over r.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, 32<<10)}
}

// Next returns the next event. It returns io.EOF when the stream ends cleanly.
// A trailing event without a terminating blank line is still dispatched.
func (r *Reader) Next() (Event, error) {
	for {
		line, err := r.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) && r.has {
				return r.dispatch(), nil
			}
			return Event{}, err
		}
		if len(line) == 0 {
			if r.has {
				return r.dispatch(), nil
			}
			continue
		}
		if line[0] == ':' {
			continue // comment
		}
		field, value := line, []byte(nil)
		if i := bytes.IndexByte(line, ':'); i >= 0 {
			field, value = line[:i], line[i+1:]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
		}
		switch string(field) {
		case "data":
			if r.dataLines > 0 {
				r.data.WriteByte('\n')
			}
			if r.data.Len()+len(value) > MaxEventBytes {
				return Event{}, ErrEventTooLarge
			}
			r.data.Write(value)
			r.dataLines++
			r.has = true
		case "event":
			r.name = string(value)
			r.has = true
		default:
			// "id", "retry" and unknown fields are ignored.
		}
	}
}

func (r *Reader) dispatch() Event {
	ev := Event{Name: r.name, Data: bytes.Clone(r.data.Bytes())}
	r.data.Reset()
	r.name = ""
	r.has = false
	r.dataLines = 0
	return ev
}

// readLine returns one line without its terminator. Lines may end in "\n",
// "\r\n" or "\r".
func (r *Reader) readLine() ([]byte, error) {
	var line []byte
	for {
		b, err := r.r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				return line, nil
			}
			return nil, err
		}
		switch b {
		case '\n':
			return line, nil
		case '\r':
			if next, err := r.r.Peek(1); err == nil && next[0] == '\n' {
				_, _ = r.r.ReadByte()
			}
			return line, nil
		}
		if len(line) >= MaxEventBytes {
			return nil, ErrEventTooLarge
		}
		line = append(line, b)
	}
}

// Writer writes SSE events and flushes after each one.
type Writer struct {
	w     io.Writer
	flush func() error
}

// NewWriter wraps an http.ResponseWriter (or any writer). If w supports
// flushing via http.ResponseController, every event is flushed immediately.
func NewWriter(w io.Writer) *Writer {
	sw := &Writer{w: w, flush: func() error { return nil }}
	if rw, ok := w.(http.ResponseWriter); ok {
		rc := http.NewResponseController(rw)
		sw.flush = rc.Flush
	}
	return sw
}

// SetHeaders sets the standard streaming response headers.
func SetHeaders(h http.Header) {
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
}

// WriteData writes one "data:" event. Payloads containing newlines are split
// across multiple data lines as the spec requires.
func (w *Writer) WriteData(data []byte) error {
	buf := make([]byte, 0, len(data)+16)
	for {
		i := bytes.IndexAny(data, "\r\n")
		buf = append(buf, "data: "...)
		if i < 0 {
			buf = append(buf, data...)
			buf = append(buf, '\n')
			break
		}
		buf = append(buf, data[:i]...)
		buf = append(buf, '\n')
		if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
			i++
		}
		data = data[i+1:]
	}
	buf = append(buf, '\n')
	if _, err := w.w.Write(buf); err != nil {
		return err
	}
	return w.flush()
}

// WriteDone writes the [DONE] sentinel.
func (w *Writer) WriteDone() error { return w.WriteData(Done) }
