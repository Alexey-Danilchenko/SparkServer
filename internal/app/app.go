// Package app composes repositories, services, HTTP routes, and TCP protocol handling.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"sparkserver/internal/auth"
	"sparkserver/internal/config"
	"sparkserver/internal/devices"
	"sparkserver/internal/events"
	"sparkserver/internal/firmware"
	"sparkserver/internal/httpapi"
	jsonfile "sparkserver/internal/jsonfile"
	"sparkserver/internal/products"
	protocoldevice "sparkserver/internal/protocol/device"
	"sparkserver/internal/protocol/handshake"
	protocolkeys "sparkserver/internal/protocol/keys"
	"sparkserver/internal/protocol/tcp"
	"sparkserver/internal/webhooks"
)

// Server owns the long-lived HTTP and TCP servers plus the domain services they share.
type Server struct {
	config   *config.Config
	logger   *slog.Logger
	auth     *auth.Service
	devices  *devices.Service
	events   *events.Service
	firmware *firmware.Service
	products *products.Service
	webhooks *webhooks.Service
	keys     *protocolkeys.Manager
	http     *httpapi.Server
	tcp      *tcp.Server
}

// New builds the file-backed application graph from legacy-compatible configuration.
func New(cfg *config.Config, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	authService := auth.NewService(
		jsonfile.NewUserRepository(cfg.UsersDirectory),
		jsonfile.NewAccessTokenRepository(cfg.TokensDirectory),
		time.Duration(cfg.AccessTokenLifetime)*time.Second,
	)
	deviceService := devices.NewService(
		jsonfile.NewDeviceRepository(cfg.DeviceDirectory),
		jsonfile.NewDeviceClaimRepository(cfg.DeviceClaimsDirectory),
		devices.WithAPITimeout(time.Duration(cfg.APITimeout)*time.Millisecond),
	)
	eventService := events.NewService(jsonfile.NewEventRepository(cfg.EventsDirectory))
	webhookService := webhooks.NewService(jsonfile.NewWebhookRepository(cfg.WebhooksDirectory))
	eventService.AddSink(webhookService)
	productDeviceRepository := jsonfile.NewProductDeviceRepository(filepath.Join(cfg.ProductsDirectory, "devices"))
	productFirmwareRepository := jsonfile.NewProductFirmwareRepository(filepath.Join(cfg.FirmwareDirectory, "metadata"))
	productService := products.NewService(
		jsonfile.NewProductRepository(cfg.ProductsDirectory),
		productDeviceRepository,
		jsonfile.NewDeviceRepository(cfg.DeviceDirectory),
		products.WithFirmwareCatalog(productFirmwareRepository),
	)
	firmwareService := firmware.NewService(
		productFirmwareRepository,
		filepath.Join(cfg.FirmwareDirectory, "binaries"),
		jsonfile.NewFlashJobRepository(filepath.Join(cfg.FirmwareDirectory, "flashJobs")),
		firmware.WithEventPublisher(eventService),
		firmware.WithProductDeviceResolver(productDeviceRepository),
	)
	keyManager := protocolkeys.NewManager(cfg.ServerKeysDirectory)
	tcpServer := newTCPServer(cfg.TCP.Address(), deviceService, eventService, firmwareService, logger.With("server", "tcp"))
	deviceService.SetLiveClient(tcpServer)
	firmwareService.SetFlashTransport(tcpServer)
	tcpServer.SetFlashSignalHandler(firmwareService)

	httpOptions := []httpapi.Option{}
	if cfg.HTTP.UseSSL {
		httpOptions = append(httpOptions, httpapi.WithTLS(httpapi.TLSConfig{
			Enabled:         true,
			CertificateFile: cfg.HTTP.SSLCertificateFilePath,
			PrivateKeyFile:  cfg.HTTP.SSLPrivateKeyFilePath,
		}))
	}

	return &Server{
		config:   cfg,
		logger:   logger,
		auth:     authService,
		devices:  deviceService,
		events:   eventService,
		firmware: firmwareService,
		products: productService,
		webhooks: webhookService,
		keys:     keyManager,
		http: httpapi.New(
			cfg.HTTP.Address(),
			httpapi.Dependencies{
				Auth:       authService,
				Devices:    deviceService,
				Events:     eventService,
				Firmware:   firmwareService,
				Products:   productService,
				Webhooks:   webhookService,
				DeviceKeys: keyManager,
			},
			logger.With("server", "http"),
			httpOptions...,
		),
		tcp: tcpServer,
	}
}

// Wait blocks until the context ends or either listener reports a terminal failure.
func (s *Server) Wait(ctx context.Context) error {
	select {
	case err := <-s.http.Errors():
		return fmt.Errorf("http server stopped: %w", err)
	case err := <-s.tcp.Errors():
		return fmt.Errorf("tcp server stopped: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newTCPServer(
	address string,
	deviceService *devices.Service,
	eventService *events.Service,
	firmwareService protocoldevice.DeviceFirmwareUpdater,
	logger *slog.Logger,
) *tcp.Server {
	server := tcp.New(address, logger)
	handler := protocoldevice.NewHandler(eventService, deviceService)
	handler.SetFirmwareUpdater(firmwareService)
	server.SetDeviceStatusUpdater(deviceService)
	server.SetMessageHandler(handler)
	return server
}

// Start prepares storage, keys, default credentials, and both network listeners.
func (s *Server) Start(ctx context.Context) error {
	if err := s.config.EnsureDirectories(); err != nil {
		return fmt.Errorf("prepare data directories: %w", err)
	}

	if err := s.keys.EnsureServerKeyPair(); err != nil {
		return fmt.Errorf("prepare server key pair: %w", err)
	}
	s.tcp.SetHandshaker(handshake.NewHandshaker(s.keys))

	if err := s.auth.EnsureDefaultAdmin(ctx, s.config.DefaultAdminUsername, s.config.DefaultAdminPassword); err != nil {
		return fmt.Errorf("prepare default administrator: %w", err)
	}

	if err := s.http.Start(); err != nil {
		return fmt.Errorf("start HTTP server: %w", err)
	}

	if err := s.tcp.Start(ctx); err != nil {
		httpErr := s.http.Shutdown(ctx)
		return errors.Join(fmt.Errorf("start TCP server: %w", err), httpErr)
	}

	s.logger.Info("spark server started", "http", s.http.ListenerAddress(), "tcp", s.tcp.ListenerAddress())
	return nil
}

// Shutdown stops device and HTTP listeners using the caller's deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("spark server stopping")
	return errors.Join(
		s.tcp.Shutdown(ctx),
		s.http.Shutdown(ctx),
	)
}
