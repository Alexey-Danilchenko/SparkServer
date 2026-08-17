package test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type sseRecorder struct {
	recorder *httptest.ResponseRecorder
	flushed  chan struct{}
	mutex    sync.Mutex
}

func newSSERecorder() *sseRecorder {
	return &sseRecorder{
		recorder: httptest.NewRecorder(),
		flushed:  make(chan struct{}, 1),
	}
}

func (recorder *sseRecorder) Header() http.Header {
	return recorder.recorder.Header()
}

func (recorder *sseRecorder) WriteHeader(statusCode int) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.recorder.WriteHeader(statusCode)
}

func (recorder *sseRecorder) Write(data []byte) (int, error) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return recorder.recorder.Write(data)
}

func (recorder *sseRecorder) Flush() {
	select {
	case recorder.flushed <- struct{}{}:
	default:
	}
}

func (recorder *sseRecorder) BodyString() string {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return recorder.recorder.Body.String()
}

func (recorder *sseRecorder) waitForFlush(t *testing.T) {
	t.Helper()
	select {
	case <-recorder.flushed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE flush")
	}
}
