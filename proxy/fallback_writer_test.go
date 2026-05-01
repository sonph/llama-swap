package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFallbackResponseWriter_502Retriable(t *testing.T) {
	rec := httptest.NewRecorder()
	fbw := newFallbackResponseWriter(rec)

	fbw.WriteHeader(http.StatusBadGateway)
	n, err := fbw.Write([]byte("error body"))

	assert.True(t, fbw.retriable)
	assert.Equal(t, http.StatusBadGateway, fbw.statusCode)
	assert.Equal(t, http.StatusOK, rec.Code) // default, not written
	assert.Equal(t, 0, rec.Body.Len())
	assert.Equal(t, 10, n)
	assert.NoError(t, err)
	assert.Equal(t, "error body", fbw.buf.String())
}

func TestFallbackResponseWriter_503Retriable(t *testing.T) {
	rec := httptest.NewRecorder()
	fbw := newFallbackResponseWriter(rec)

	fbw.WriteHeader(http.StatusServiceUnavailable)
	fbw.Write([]byte("unavailable")) //nolint:errcheck // best effort

	assert.True(t, fbw.retriable)
	assert.Equal(t, http.StatusServiceUnavailable, fbw.statusCode)
	assert.Equal(t, http.StatusOK, rec.Code) // default, not written
	assert.Equal(t, 0, rec.Body.Len())
}

func TestFallbackResponseWriter_200NotRetriable(t *testing.T) {
	rec := httptest.NewRecorder()
	fbw := newFallbackResponseWriter(rec)

	fbw.WriteHeader(http.StatusOK)
	fbw.Write([]byte("success")) //nolint:errcheck // best effort

	assert.False(t, fbw.retriable)
	assert.True(t, fbw.committed)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "success", rec.Body.String())
}

func TestFallbackResponseWriter_429NotRetriable(t *testing.T) {
	rec := httptest.NewRecorder()
	fbw := newFallbackResponseWriter(rec)

	fbw.WriteHeader(http.StatusTooManyRequests)
	fbw.Write([]byte("rate limited")) //nolint:errcheck // best effort

	assert.False(t, fbw.retriable)
	assert.True(t, fbw.committed)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "rate limited", rec.Body.String())
}

func TestFallbackResponseWriter_FlushErrorToWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	fbw := newFallbackResponseWriter(rec)

	fbw.WriteHeader(http.StatusBadGateway)
	fbw.Write([]byte("error from last candidate")) //nolint:errcheck // best effort

	// Flush to a fresh writer
	finalRec := httptest.NewRecorder()
	fbw.flushErrorToWriter(finalRec)

	assert.Equal(t, http.StatusBadGateway, finalRec.Code)
	assert.Equal(t, "error from last candidate", finalRec.Body.String())
}

func TestFallbackResponseWriter_WriteWithoutHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	fbw := newFallbackResponseWriter(rec)

	// Write without calling WriteHeader first
	fbw.Write([]byte("body only")) //nolint:errcheck // best effort

	assert.False(t, fbw.retriable)
	assert.True(t, fbw.committed)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "body only", rec.Body.String())
}

func TestFallbackResponseWriter_Flush(t *testing.T) {
	rec := httptest.NewRecorder()
	fbw := newFallbackResponseWriter(rec)

	fbw.WriteHeader(http.StatusOK)
	fbw.Flush() // should not panic

	assert.True(t, fbw.committed)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestFallbackResponseWriter_FlushRetriable(t *testing.T) {
	rec := httptest.NewRecorder()
	fbw := newFallbackResponseWriter(rec)

	fbw.WriteHeader(http.StatusBadGateway)
	fbw.Flush() // should be a no-op for retriable

	assert.True(t, fbw.retriable)
	assert.False(t, fbw.committed)
	assert.Equal(t, http.StatusOK, rec.Code) // default, not written
}

func TestFallbackResponseWriter_CompatibilityMethods(t *testing.T) {
	rec := httptest.NewRecorder()
	fbw := newFallbackResponseWriter(rec)

	fbw.WriteHeader(http.StatusBadGateway)

	// Test Status()
	assert.Equal(t, http.StatusBadGateway, fbw.Status())

	// Test Size() for retriable
	fbw.Write([]byte("12345")) //nolint:errcheck // best effort
	assert.Equal(t, 5, fbw.Size())

	// Test Written()
	assert.False(t, fbw.Written())

	// Test WriteString()
	n, err := fbw.WriteString("hello")
	assert.NoError(t, err)
	assert.Equal(t, 5, n)
}

func TestIsFallbackRetriable(t *testing.T) {
	assert.True(t, isFallbackRetriable(http.StatusBadGateway))
	assert.True(t, isFallbackRetriable(http.StatusServiceUnavailable))
	assert.False(t, isFallbackRetriable(http.StatusOK))
	assert.False(t, isFallbackRetriable(http.StatusNotFound))
	assert.False(t, isFallbackRetriable(http.StatusTooManyRequests))
	assert.False(t, isFallbackRetriable(http.StatusInternalServerError))
}
