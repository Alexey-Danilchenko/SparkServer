// Package tcp owns the Particle-compatible device TCP listener.
package tcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"sparkserver/internal/netutil"
	"sparkserver/internal/protocol/coap"
	"sparkserver/internal/protocol/session"
)

// DeviceStatusUpdater is implemented by devices.Service to persist online state.
type DeviceStatusUpdater interface {
	MarkConnected(ctx context.Context, deviceID string) error
	MarkDisconnected(ctx context.Context, deviceID string) error
}

// Handshaker establishes the encrypted session from the first device frame.
type Handshaker interface {
	Handshake(ctx context.Context, reader io.Reader) (*session.Session, error)
}

// MessageHandler handles decrypted device CoAP packets not claimed by pending requests.
type MessageHandler interface {
	Handle(ctx context.Context, session *session.Session, packet *coap.Packet) (*coap.Packet, error)
}

// AfterResponseHandler runs follow-up work after an ACK is sent to the device.
type AfterResponseHandler interface {
	AfterResponse(ctx context.Context, session *session.Session, packet *coap.Packet)
}

// Server accepts device TCP connections and maintains live command clients.
type Server struct {
	address         string
	listenerAddress atomic.Value
	logger          *slog.Logger
	listener        net.Listener
	done            chan struct{}
	runtimeErrors   chan error
	registry        *Registry
	deviceStatus    DeviceStatusUpdater
	handshaker      Handshaker
	handler         MessageHandler
	flashSignals    FlashSignalHandler
	connections     map[net.Conn]struct{}
	connectionWG    sync.WaitGroup
	stopping        bool
	mu              sync.Mutex
}

// New creates a TCP server with an empty device registry.
func New(address string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	return &Server{
		address:       address,
		logger:        logger,
		done:          make(chan struct{}),
		runtimeErrors: make(chan error, 1),
		registry:      NewRegistry(),
		connections:   make(map[net.Conn]struct{}),
	}
}

func (s *Server) SetDeviceStatusUpdater(deviceStatus DeviceStatusUpdater) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.deviceStatus = deviceStatus
}

func (s *Server) SetHandshaker(handshaker Handshaker) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.handshaker = handshaker
}

func (s *Server) SetMessageHandler(handler MessageHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.handler = handler
}

func (s *Server) SetFlashSignalHandler(handler FlashSignalHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.flashSignals = handler
}

func (s *Server) Registry() *Registry {
	return s.registry
}

func (s *Server) RegisterDevice(ctx context.Context, deviceID string, conn net.Conn) func() {
	s.registry.Register(deviceID, conn.RemoteAddr())

	s.mu.Lock()
	deviceStatus := s.deviceStatus
	s.mu.Unlock()

	if deviceStatus != nil {
		if err := deviceStatus.MarkConnected(ctx, deviceID); err != nil {
			s.logger.Error("mark device connected", "device_id", deviceID, "err", err)
		}
	}

	return func() {
		s.registry.Unregister(deviceID)

		if deviceStatus != nil {
			if err := deviceStatus.MarkDisconnected(context.WithoutCancel(ctx), deviceID); err != nil {
				s.logger.Error("mark device disconnected", "device_id", deviceID, "err", err)
			}
		}
	}
}

// RegisterDeviceClient records a live command-capable device and returns cleanup.
func (s *Server) RegisterDeviceClient(
	ctx context.Context,
	deviceID string,
	conn net.Conn,
	client *Client,
) func() {
	s.registry.RegisterClient(deviceID, conn.RemoteAddr(), client)

	s.mu.Lock()
	deviceStatus := s.deviceStatus
	flashSignals := s.flashSignals
	s.mu.Unlock()

	client.SetFlashSignalHandler(flashSignals)

	if deviceStatus != nil {
		if err := deviceStatus.MarkConnected(ctx, deviceID); err != nil {
			s.logger.Error("mark device connected", "device_id", deviceID, "err", err)
		}
	}

	return func() {
		s.registry.Unregister(deviceID)

		if deviceStatus != nil {
			if err := deviceStatus.MarkDisconnected(context.WithoutCancel(ctx), deviceID); err != nil {
				s.logger.Error("mark device disconnected", "device_id", deviceID, "err", err)
			}
		}
	}
}

func (s *Server) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("listen for device connections on %s: %w", s.address, err)
	}

	listenerAddress := netutil.AdvertisedAddress(listener.Addr())
	s.listenerAddress.Store(listenerAddress)

	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()

	s.logger.Info("tcp listener started", "address", listenerAddress)
	go s.accept(ctx, listener)
	return nil
}

// Errors reports terminal runtime failures from the TCP listener.
func (s *Server) Errors() <-chan error {
	return s.runtimeErrors
}

// ListenerAddress returns the reachable address reported for the active listener.
func (s *Server) ListenerAddress() string {
	address := s.listenerAddress.Load()
	if address == nil {
		return ""
	}

	return address.(string)
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.stopping = true
	listener := s.listener
	connections := make([]net.Conn, 0, len(s.connections))
	for connection := range s.connections {
		connections = append(connections, connection)
	}
	s.mu.Unlock()

	if listener != nil {
		_ = listener.Close()
	}
	for _, connection := range connections {
		_ = connection.Close()
	}

	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) accept(ctx context.Context, listener net.Listener) {
	defer func() {
		s.connectionWG.Wait()
		close(s.done)
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			s.runtimeErrors <- fmt.Errorf("accept device connection: %w", err)
			return
		}

		if !s.trackConnection(conn) {
			_ = conn.Close()
			continue
		}
		s.connectionWG.Go(func() {
			defer s.untrackConnection(conn)
			s.handleConnection(ctx, conn)
		})
	}
}

func (s *Server) trackConnection(connection net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return false
	}
	s.connections[connection] = struct{}{}
	return true
}

func (s *Server) untrackConnection(connection net.Conn) {
	s.mu.Lock()
	delete(s.connections, connection)
	s.mu.Unlock()
}

func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	s.logger.Info("device connection accepted", "remote", conn.RemoteAddr().String())
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	s.mu.Lock()
	handshaker := s.handshaker
	handler := s.handler
	s.mu.Unlock()

	var cleanup func()
	var deviceSession *session.Session
	var deviceClient *Client
	if handshaker != nil {
		handshakeSession, err := handshaker.Handshake(ctx, conn)
		if err != nil {
			s.logger.Error("device handshake failed", "remote", conn.RemoteAddr().String(), "err", err)
			return
		}
		deviceSession = handshakeSession

		deviceClient, err = NewClient(deviceSession, conn)
		if err != nil {
			s.logger.Error("device client setup failed", "device_id", deviceSession.DeviceID, "err", err)
			return
		}

		cleanup = s.RegisterDeviceClient(ctx, deviceSession.DeviceID, conn, deviceClient)
		defer cleanup()
		s.logger.Info("device handshake completed", "device_id", deviceSession.DeviceID, "remote", conn.RemoteAddr().String())
	}

	if deviceSession != nil && deviceClient != nil && handler != nil {
		if err := s.handleSession(ctx, conn, deviceSession, deviceClient, handler); err != nil && ctx.Err() == nil {
			s.logger.Error("device session stopped", "device_id", deviceSession.DeviceID, "err", err)
		}
		return
	}

	_, _ = io.Copy(io.Discard, conn)
}

func (s *Server) handleSession(
	ctx context.Context,
	conn net.Conn,
	deviceSession *session.Session,
	deviceClient *Client,
	handler MessageHandler,
) error {
	_ = conn.SetDeadline(time.Time{})
	return ServeSession(ctx, conn, deviceSession, deviceClient, handler, func(deviceID string) {
		s.registry.Touch(deviceID)
	})
}
