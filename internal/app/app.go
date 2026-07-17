// Package app composes repositories, services, HTTP routes, and TCP protocol handling.
package app

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"sparkserver/internal/auth"
	"sparkserver/internal/config"
	"sparkserver/internal/devices"
	"sparkserver/internal/events"
	"sparkserver/internal/firmware"
	"sparkserver/internal/httpapi"
	"sparkserver/internal/products"
	protocoldevice "sparkserver/internal/protocol/device"
	"sparkserver/internal/protocol/handshake"
	protocolkeys "sparkserver/internal/protocol/keys"
	"sparkserver/internal/protocol/tcp"
	filerepo "sparkserver/internal/repository/file"
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
		filerepo.NewUserRepository(cfg.UsersDirectory),
		filerepo.NewAccessTokenRepository(cfg.TokensDirectory),
		time.Duration(cfg.AccessTokenLifetime)*time.Second,
	)
	deviceService := devices.NewService(
		filerepo.NewDeviceRepository(cfg.DeviceDirectory),
		filerepo.NewDeviceClaimRepository(cfg.DeviceClaimsDirectory),
	)
	deviceService.SetAPITimeout(time.Duration(cfg.APITimeout) * time.Millisecond)
	eventService := events.NewService(filerepo.NewEventRepository(cfg.EventsDirectory))
	webhookService := webhooks.NewService(filerepo.NewWebhookRepository(cfg.WebhooksDirectory))
	eventService.AddSink(webhookService)
	productDeviceRepository := filerepo.NewProductDeviceRepository(filepath.Join(cfg.ProductsDirectory, "devices"))
	productFirmwareRepository := filerepo.NewProductFirmwareRepository(filepath.Join(cfg.FirmwareDirectory, "metadata"))
	productService := products.NewService(
		filerepo.NewProductRepository(cfg.ProductsDirectory),
		productDeviceRepository,
		filerepo.NewDeviceRepository(cfg.DeviceDirectory),
	)
	productService.SetProductFirmwareRepository(productFirmwareRepository)
	firmwareService := firmware.NewService(
		productFirmwareRepository,
		filepath.Join(cfg.FirmwareDirectory, "binaries"),
		filerepo.NewFlashJobRepository(filepath.Join(cfg.FirmwareDirectory, "flashJobs")),
	)
	keyManager := protocolkeys.NewManager(cfg.ServerKeysDirectory)
	tcpServer := newTCPServer(cfg.TCP.Address(), deviceService, eventService, firmwareService, logger.With("server", "tcp"))
	deviceService.SetLiveClient(tcpServer)
	firmwareService.SetFlashTransport(tcpServer)
	firmwareService.SetEventPublisher(eventService)
	firmwareService.SetProductDeviceResolver(productDeviceRepository)
	tcpServer.SetFlashSignalHandler(firmwareService)

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
		http:     httpapi.NewWithDeviceKeys(cfg.HTTP.Address(), authService, deviceService, eventService, firmwareService, productService, webhookService, keyManager, logger.With("server", "http")),
		tcp:      tcpServer,
	}
}

func newTCPServer(
	address         string,
	deviceService   *devices.Service,
	eventService    *events.Service,
	firmwareService protocoldevice.DeviceFirmwareUpdater,
	logger          *slog.Logger,
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
		return err
	}

	if err := s.keys.EnsureServerKeyPair(); err != nil {
		return err
	}
	s.tcp.SetHandshaker(handshake.NewHandshaker(s.keys))

	if err := s.auth.EnsureDefaultAdmin(ctx, s.config.DefaultAdminUsername, s.config.DefaultAdminPassword); err != nil {
		return err
	}

	if err := s.http.Start(); err != nil {
		return err
	}

	if err := s.tcp.Start(ctx); err != nil {
		httpErr := s.http.Shutdown(ctx)
		return errors.Join(err, httpErr)
	}

	s.logger.Info("spark server started", "http", s.config.HTTP.Address(), "tcp", s.config.TCP.Address())
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
