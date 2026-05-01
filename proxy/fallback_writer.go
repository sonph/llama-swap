package proxy

import (
	"bufio"
	"bytes"
	"net"
	"net/http"
)

// fallbackResponseWriter wraps an http.ResponseWriter to intercept responses
// for fallback routing. On retriable errors (502, 503), it buffers the response
// instead of writing to the client, allowing the next fallback candidate to be tried.
// On non-retriable responses (success or client errors), it writes through immediately.
type fallbackResponseWriter struct {
	http.ResponseWriter
	statusCode int
	buf        bytes.Buffer
	retriable  bool
	committed  bool
}

func newFallbackResponseWriter(w http.ResponseWriter) *fallbackResponseWriter {
	return &fallbackResponseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}
}

// isFallbackRetriable returns true for status codes that warrant trying the next fallback model.
// 502 = process failed to start or peer connection refused
// 503 = process is stopping/shutdown
func isFallbackRetriable(code int) bool {
	return code == http.StatusBadGateway || code == http.StatusServiceUnavailable
}

// WriteHeader intercepts the status code to decide whether to buffer or commit.
func (f *fallbackResponseWriter) WriteHeader(code int) {
	f.statusCode = code
	f.retriable = isFallbackRetriable(code)

	// If not retriable, commit headers immediately
	if !f.retriable && !f.committed {
		f.committed = true
		f.ResponseWriter.WriteHeader(code)
	}
}

// Write buffers the body for retriable errors, otherwise writes through.
func (f *fallbackResponseWriter) Write(b []byte) (int, error) {
	if f.retriable {
		return f.buf.Write(b)
	}
	if !f.committed {
		f.committed = true
		f.ResponseWriter.WriteHeader(f.statusCode)
	}
	return f.ResponseWriter.Write(b)
}

// Flush forwards to the underlying http.Flusher if available.
// For retriable errors, nothing is flushed (response is buffered).
func (f *fallbackResponseWriter) Flush() {
	if f.retriable {
		return
	}
	if !f.committed {
		f.committed = true
		f.ResponseWriter.WriteHeader(f.statusCode)
	}
	if flusher, ok := f.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// flushErrorToWriter writes the buffered status code and body to the provided writer.
// Used when all fallback candidates have been exhausted.
func (f *fallbackResponseWriter) flushErrorToWriter(w http.ResponseWriter) {
	w.WriteHeader(f.statusCode)
	f.buf.WriteTo(w) //nolint:errcheck // best effort on final error
}

// Hijack implements net.Conn hijacking for websocket/SSE upgrades.
// Only valid for non-retriable (committed) responses.
func (f *fallbackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := f.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// CloseNotify implements http.CloseNotifier for compatibility.
// Returns a no-op channel since close notifications are not supported
// in the fallback path (HTTP/1.1 CloseNotifier is deprecated in HTTP/2).
func (f *fallbackResponseWriter) CloseNotify() <-chan bool {
	return make(chan bool, 1)
}

// Pusher returns the Pusher for the underlying ResponseWriter if available.
func (f *fallbackResponseWriter) Pusher() http.Pusher {
	if pusher, ok := f.ResponseWriter.(http.Pusher); ok {
		return pusher
	}
	return nil
}

// Size returns the number of bytes written for gin.ResponseWriter compatibility.
func (f *fallbackResponseWriter) Size() int {
	if f.retriable {
		return f.buf.Len()
	}
	// Forward to underlying writer's Size if it has one
	if sized, ok := f.ResponseWriter.(interface{ Size() int }); ok {
		return sized.Size()
	}
	return 0
}

// Status returns the status code for gin.ResponseWriter compatibility.
func (f *fallbackResponseWriter) Status() int {
	return f.statusCode
}

// WriteHeaderNow commits headers immediately without writing the body.
func (f *fallbackResponseWriter) WriteHeaderNow() {
	if !f.committed && !f.retriable {
		f.committed = true
		f.ResponseWriter.WriteHeader(f.statusCode)
	}
}

// WriteString writes a string, implementing io.StringWriter.
func (f *fallbackResponseWriter) WriteString(s string) (int, error) {
	return f.Write([]byte(s))
}

// Written returns whether headers have been written.
func (f *fallbackResponseWriter) Written() bool {
	return f.committed
}
