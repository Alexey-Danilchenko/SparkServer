package tcp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"
)

func TestAcceptReportsTerminalError(t *testing.T) {
	want := errors.New("listener failed")
	listener := newScriptedListener()
	listener.accepts <- acceptResult{err: want}
	server := New(":0", slog.New(slog.NewTextHandler(io.Discard, nil)))

	go server.accept(t.Context(), listener)

	select {
	case err := <-server.Errors():
		if !errors.Is(err, want) {
			t.Fatalf("runtime error = %v, want %v", err, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for listener error")
	}
}

func TestShutdownClosesActiveConnections(t *testing.T) {
	listener := newScriptedListener()
	server := New(":0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	server.mu.Lock()
	server.listener = listener
	server.mu.Unlock()

	serverConnection, clientConnection := net.Pipe()
	t.Cleanup(func() { _ = clientConnection.Close() })
	readStarted := make(chan struct{})
	listener.accepts <- acceptResult{connection: &observedConn{Conn: serverConnection, readStarted: readStarted}}
	go server.accept(t.Context(), listener)

	select {
	case <-readStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for active connection")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

type acceptResult struct {
	connection net.Conn
	err        error
}

type scriptedListener struct {
	accepts chan acceptResult
	closed  chan struct{}
	once    sync.Once
}

func newScriptedListener() *scriptedListener {
	return &scriptedListener{
		accepts: make(chan acceptResult, 1),
		closed:  make(chan struct{}),
	}
}

func (listener *scriptedListener) Accept() (net.Conn, error) {
	select {
	case result := <-listener.accepts:
		return result.connection, result.err
	case <-listener.closed:
		return nil, net.ErrClosed
	}
}

func (listener *scriptedListener) Close() error {
	listener.once.Do(func() { close(listener.closed) })
	return nil
}

func (listener *scriptedListener) Addr() net.Addr {
	return testAddress("listener")
}

type testAddress string

func (address testAddress) Network() string { return "test" }
func (address testAddress) String() string  { return string(address) }

type observedConn struct {
	net.Conn
	readStarted chan struct{}
	once        sync.Once
}

func (connection *observedConn) Read(data []byte) (int, error) {
	connection.once.Do(func() { close(connection.readStarted) })
	return connection.Conn.Read(data)
}
