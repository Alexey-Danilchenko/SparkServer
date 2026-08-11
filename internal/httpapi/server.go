// Package httpapi exposes Spark/Particle-compatible REST and SSE routes.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"sparkserver/internal/auth"
	"sparkserver/internal/devices"
	"sparkserver/internal/domain"
	"sparkserver/internal/events"
	"sparkserver/internal/netutil"
	"sparkserver/internal/repository"
)

// DeviceKeyRegistrar stores public keys submitted by Particle device provisioning.
type DeviceKeyRegistrar interface {
	SaveDevicePublicKeyPEM(deviceID string, publicKeyPEM string) error
}

// Server wraps net/http with the configured route tree and lifecycle methods.
type Server struct {
	address         string
	listenerAddress atomic.Value
	logger          *slog.Logger
	server          *http.Server
}

// New builds an HTTP server with the core auth/device/event API surface.
func New(
	address       string,
	authService   *auth.Service,
	deviceService *devices.Service,
	eventService  *events.Service,
	logger        *slog.Logger,
) *Server {
	return NewWithFirmware(address, authService, deviceService, eventService, nil, logger)
}

func NewWithFirmware(
	address         string,
	authService     *auth.Service,
	deviceService   *devices.Service,
	eventService    *events.Service,
	firmwareService FirmwareService,
	logger          *slog.Logger,
) *Server {
	return NewWithFirmwareAndProducts(address, authService, deviceService, eventService, firmwareService, nil, logger)
}

func NewWithFirmwareAndProducts(
	address         string,
	authService     *auth.Service,
	deviceService   *devices.Service,
	eventService    *events.Service,
	firmwareService FirmwareService,
	productService  ProductService,
	logger          *slog.Logger,
) *Server {
	return NewWithServices(address, authService, deviceService, eventService, firmwareService, productService, nil, logger)
}

func NewWithServices(
	address         string,
	authService     *auth.Service,
	deviceService   *devices.Service,
	eventService    *events.Service,
	firmwareService FirmwareService,
	productService  ProductService,
	webhookService  WebhookService,
	logger          *slog.Logger,
) *Server {
	return NewWithDeviceKeys(address, authService, deviceService, eventService, firmwareService, productService, webhookService, nil, logger)
}

// NewWithDeviceKeys builds the full file-backed HTTP API including provisioning keys.
func NewWithDeviceKeys(
	address         string,
	authService     *auth.Service,
	deviceService   *devices.Service,
	eventService    *events.Service,
	firmwareService FirmwareService,
	productService  ProductService,
	webhookService  WebhookService,
	keyRegistrar    DeviceKeyRegistrar,
	logger          *slog.Logger,
) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	return &Server{
		address: address,
		logger:  logger,
		server: &http.Server{
			Addr:              address,
			Handler:           NewHandlerWithDeviceKeys(authService, deviceService, eventService, firmwareService, productService, webhookService, keyRegistrar, logger),
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

func NewHandler(
	authService   *auth.Service,
	deviceService *devices.Service,
	eventService  *events.Service,
	logger        *slog.Logger,
) http.Handler {
	return NewHandlerWithFirmware(authService, deviceService, eventService, nil, logger)
}

func NewHandlerWithFirmware(
	authService     *auth.Service,
	deviceService   *devices.Service,
	eventService    *events.Service,
	firmwareService FirmwareService,
	logger          *slog.Logger,
) http.Handler {
	return NewHandlerWithFirmwareAndProducts(authService, deviceService, eventService, firmwareService, nil, logger)
}

func NewHandlerWithFirmwareAndProducts(
	authService     *auth.Service,
	deviceService   *devices.Service,
	eventService    *events.Service,
	firmwareService FirmwareService,
	productService  ProductService,
	logger          *slog.Logger,
) http.Handler {
	return NewHandlerWithServices(authService, deviceService, eventService, firmwareService, productService, nil, logger)
}

func NewHandlerWithServices(
	authService     *auth.Service,
	deviceService   *devices.Service,
	eventService    *events.Service,
	firmwareService FirmwareService,
	productService  ProductService,
	webhookService  WebhookService,
	logger          *slog.Logger,
) http.Handler {
	return NewHandlerWithDeviceKeys(authService, deviceService, eventService, firmwareService, productService, webhookService, nil, logger)
}

// NewHandlerWithDeviceKeys registers v1/v2 compatibility routes onto a ServeMux.
func NewHandlerWithDeviceKeys(
	authService     *auth.Service,
	deviceService   *devices.Service,
	eventService    *events.Service,
	firmwareService FirmwareService,
	productService  ProductService,
	webhookService  WebhookService,
	keyRegistrar    DeviceKeyRegistrar,
	logger          *slog.Logger,
) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	router := http.NewServeMux()
	// Keep route registration explicit so COMPATIBILITY.md can be checked against it.
	router.HandleFunc("GET /health", healthHandler)
	router.HandleFunc("GET /", rootHandler)
	router.HandleFunc("POST /oauth/token", tokenHandler(authService))
	router.HandleFunc("POST /v1/ping", pingHandler)
	router.HandleFunc("POST /v2/ping", pingHandler)
	router.HandleFunc("POST /v1/users", createUserHandler(authService))
	router.Handle("GET /v1/access_tokens", requireAuth(authService, http.HandlerFunc(listTokensHandler(authService))))
	router.Handle("DELETE /v1/access_tokens/{token}", requireAuth(authService, http.HandlerFunc(deleteTokenHandler(authService))))
	router.Handle("POST /v1/device_claims", requireAuth(authService, http.HandlerFunc(createDeviceClaimHandler(deviceService))))
	router.HandleFunc("POST /v1/provisioning/{deviceID}", provisionDeviceHandler(authService, deviceService, keyRegistrar))
	router.Handle("POST /v1/devices", requireAuth(authService, http.HandlerFunc(claimDeviceHandler(deviceService))))
	router.Handle("GET /v1/devices", requireAuth(authService, http.HandlerFunc(listDevicesHandler(deviceService))))
	router.Handle("GET /v1/devices/{deviceIDorName}", requireAuth(authService, http.HandlerFunc(getDeviceHandler(deviceService))))
	router.Handle("GET /v1/devices/{deviceIDorName}/flash", requireAuth(authService, http.HandlerFunc(listDeviceFlashJobsHandler(deviceService, firmwareService))))
	router.Handle("POST /v1/devices/{deviceIDorName}/flash", requireAuth(authService, http.HandlerFunc(startDeviceFlashHandler(deviceService, firmwareService))))
	router.Handle("GET /v1/devices/{deviceIDorName}/flash/{jobID}", requireAuth(authService, http.HandlerFunc(getDeviceFlashJobHandler(deviceService, firmwareService))))
	router.Handle("GET /v1/devices/{deviceIDorName}/{varName}", requireAuth(authService, http.HandlerFunc(getDeviceVariableHandler(deviceService))))
	router.Handle("POST /v1/devices/{deviceIDorName}/{functionName}", requireAuth(authService, http.HandlerFunc(callDeviceFunctionHandler(deviceService))))
	router.Handle("PUT /v1/devices/{deviceIDorName}", requireAuth(authService, http.HandlerFunc(updateDeviceHandler(deviceService, firmwareService))))
	router.Handle("DELETE /v1/devices/{deviceIDorName}", requireAuth(authService, http.HandlerFunc(deleteDeviceHandler(deviceService))))
	router.Handle("PUT /v1/devices/{deviceIDorName}/ping", requireAuth(authService, http.HandlerFunc(pingDeviceHandler(deviceService))))
	router.Handle("GET /v1/events", requireAuth(authService, http.HandlerFunc(streamEventsHandler(eventService, events.Filter{}))))
	router.Handle("GET /v1/events/{prefix...}", requireAuth(authService, http.HandlerFunc(streamEventsFromPathHandler(eventService, ""))))
	router.Handle("GET /v1/devices/events", requireAuth(authService, http.HandlerFunc(streamEventsHandler(eventService, events.Filter{}))))
	router.Handle("GET /v1/devices/{deviceIDorName}/events", requireAuth(authService, http.HandlerFunc(streamDeviceEventsHandler(eventService))))
	router.Handle("GET /v1/devices/{deviceIDorName}/events/{prefix...}", requireAuth(authService, http.HandlerFunc(streamDeviceEventsHandler(eventService))))
	router.Handle("POST /v1/devices/events", requireAuth(authService, http.HandlerFunc(publishEventHandler(eventService))))
	router.Handle("GET /v2/events", requireAuth(authService, http.HandlerFunc(streamEventsHandler(eventService, events.Filter{}))))
	router.Handle("GET /v2/events/{prefix...}", requireAuth(authService, http.HandlerFunc(streamEventsFromPathHandler(eventService, ""))))
	router.Handle("GET /v2/devices/events", requireAuth(authService, http.HandlerFunc(streamEventsHandler(eventService, events.Filter{}))))
	router.Handle("GET /v2/devices/{deviceIDorName}/events", requireAuth(authService, http.HandlerFunc(streamDeviceEventsHandler(eventService))))
	router.Handle("GET /v2/devices/{deviceIDorName}/events/{prefix...}", requireAuth(authService, http.HandlerFunc(streamDeviceEventsHandler(eventService))))
	router.Handle("POST /v2/devices/events", requireAuth(authService, http.HandlerFunc(publishEventHandler(eventService))))
	router.Handle("GET /v2/devices/{deviceIDorName}/flash", requireAuth(authService, http.HandlerFunc(listDeviceFlashJobsHandler(deviceService, firmwareService))))
	router.Handle("POST /v2/devices/{deviceIDorName}/flash", requireAuth(authService, http.HandlerFunc(startDeviceFlashHandler(deviceService, firmwareService))))
	router.Handle("GET /v2/devices/{deviceIDorName}/flash/{jobID}", requireAuth(authService, http.HandlerFunc(getDeviceFlashJobHandler(deviceService, firmwareService))))
	router.Handle("GET /v1/products/{productIDOrSlug}/firmware", requireAuth(authService, http.HandlerFunc(listProductFirmwaresHandler(firmwareService))))
	router.Handle("POST /v1/products/{productIDOrSlug}/firmware", requireAuth(authService, http.HandlerFunc(uploadProductFirmwareHandler(firmwareService))))
	router.Handle("GET /v1/products/{productIDOrSlug}/firmware/check", requireAuth(authService, http.HandlerFunc(checkProductFirmwareUpdateHandler(firmwareService))))
	router.Handle("GET /v1/products/{productIDOrSlug}/firmware/{firmwareID}", requireAuth(authService, http.HandlerFunc(getProductFirmwareHandler(firmwareService))))
	router.Handle("PUT /v1/products/{productIDOrSlug}/firmware/{firmwareID}", requireAuth(authService, http.HandlerFunc(updateProductFirmwareHandler(firmwareService))))
	router.Handle("DELETE /v1/products/{productIDOrSlug}/firmware/{firmwareID}", requireAuth(authService, http.HandlerFunc(deleteProductFirmwareHandler(firmwareService))))
	router.Handle("PUT /v1/products/{productIDOrSlug}/firmware/{firmwareID}/release", requireAuth(authService, http.HandlerFunc(releaseProductFirmwareHandler(firmwareService))))
	router.Handle("POST /v1/products/{productIDOrSlug}/firmware/{firmwareID}/release", requireAuth(authService, http.HandlerFunc(releaseProductFirmwareHandler(firmwareService))))
	router.Handle("PUT /v1/products/{productIDOrSlug}/firmware/{firmwareID}/default", requireAuth(authService, http.HandlerFunc(defaultProductFirmwareHandler(firmwareService))))
	router.Handle("POST /v1/products/{productIDOrSlug}/firmware/{firmwareID}/default", requireAuth(authService, http.HandlerFunc(defaultProductFirmwareHandler(firmwareService))))
	router.Handle("GET /v2/products/{productIDOrSlug}/firmwares", requireAuth(authService, http.HandlerFunc(listProductFirmwaresHandler(firmwareService))))
	router.Handle("POST /v2/products/{productIDOrSlug}/firmwares", requireAuth(authService, http.HandlerFunc(uploadProductFirmwareHandler(firmwareService))))
	router.Handle("GET /v2/products/{productIDOrSlug}/firmwares/count", requireAuth(authService, http.HandlerFunc(countProductFirmwaresHandler(firmwareService))))
	router.Handle("GET /v2/products/{productIDOrSlug}/firmwares/check", requireAuth(authService, http.HandlerFunc(checkProductFirmwareUpdateHandler(firmwareService))))
	router.Handle("GET /v2/products/{productIDOrSlug}/firmwares/{firmwareID}", requireAuth(authService, http.HandlerFunc(getProductFirmwareHandler(firmwareService))))
	router.Handle("PUT /v2/products/{productIDOrSlug}/firmwares/{firmwareID}", requireAuth(authService, http.HandlerFunc(updateProductFirmwareHandler(firmwareService))))
	router.Handle("DELETE /v2/products/{productIDOrSlug}/firmwares/{firmwareID}", requireAuth(authService, http.HandlerFunc(deleteProductFirmwareHandler(firmwareService))))
	router.Handle("PUT /v2/products/{productIDOrSlug}/firmwares/{firmwareID}/release", requireAuth(authService, http.HandlerFunc(releaseProductFirmwareHandler(firmwareService))))
	router.Handle("POST /v2/products/{productIDOrSlug}/firmwares/{firmwareID}/release", requireAuth(authService, http.HandlerFunc(releaseProductFirmwareHandler(firmwareService))))
	router.Handle("PUT /v2/products/{productIDOrSlug}/firmwares/{firmwareID}/default", requireAuth(authService, http.HandlerFunc(defaultProductFirmwareHandler(firmwareService))))
	router.Handle("POST /v2/products/{productIDOrSlug}/firmwares/{firmwareID}/default", requireAuth(authService, http.HandlerFunc(defaultProductFirmwareHandler(firmwareService))))
	router.Handle("GET /v2/products/count", requireAuth(authService, http.HandlerFunc(countProductsHandler(productService))))
	router.Handle("GET /v1/products", requireAuth(authService, http.HandlerFunc(listProductsHandler(productService))))
	router.Handle("POST /v1/products", requireAuth(authService, http.HandlerFunc(createProductHandler(productService))))
	router.Handle("GET /v1/products/{productIDOrSlug}/config", requireAuth(authService, http.HandlerFunc(getProductConfigHandler(productService))))
	router.Handle("GET /v1/products/{productIDOrSlug}/events", requireAuth(authService, http.HandlerFunc(streamProductEventsHandler(eventService, productService))))
	router.Handle("GET /v1/products/{productIDOrSlug}/events/{prefix...}", requireAuth(authService, http.HandlerFunc(streamProductEventsHandler(eventService, productService))))
	router.Handle("DELETE /v1/products/{productIDOrSlug}/team/{username}", requireAuth(authService, http.HandlerFunc(unsupportedProductFeatureHandler("not_supported"))))
	router.Handle("POST /v1/products/{productIDOrSlug}/clients", requireAuth(authService, http.HandlerFunc(unsupportedProductFeatureHandler("not_supported"))))
	router.Handle("POST /v1/products/{productIDOrSlug}/clients/", requireAuth(authService, http.HandlerFunc(unsupportedProductFeatureHandler("not_supported"))))
	router.Handle("PUT /v1/products/{productIDOrSlug}/clients/{clientID}", requireAuth(authService, http.HandlerFunc(unsupportedProductFeatureHandler("not_supported"))))
	router.Handle("DELETE /v1/products/{productIDOrSlug}/clients/{clientID}", requireAuth(authService, http.HandlerFunc(unsupportedProductFeatureHandler("not_supported"))))
	router.Handle("GET /v1/products/{productIDOrSlug}", requireAuth(authService, http.HandlerFunc(getProductHandler(productService))))
	router.Handle("PUT /v1/products/{productIDOrSlug}", requireAuth(authService, http.HandlerFunc(updateProductHandler(productService))))
	router.Handle("DELETE /v1/products/{productIDOrSlug}", requireAuth(authService, http.HandlerFunc(deleteProductHandler(productService))))
	router.Handle("GET /v1/products/{productIDOrSlug}/devices", requireAuth(authService, http.HandlerFunc(listProductDevicesHandler(productService))))
	router.Handle("POST /v1/products/{productIDOrSlug}/devices", requireAuth(authService, http.HandlerFunc(addProductDeviceHandler(productService))))
	router.Handle("GET /v1/products/{productIDOrSlug}/devices/{deviceID}", requireAuth(authService, http.HandlerFunc(getProductDeviceHandler(productService))))
	router.Handle("PUT /v1/products/{productIDOrSlug}/devices/{deviceID}", requireAuth(authService, http.HandlerFunc(updateProductDeviceHandler(productService, firmwareService))))
	router.Handle("DELETE /v1/products/{productIDOrSlug}/devices/{deviceID}", requireAuth(authService, http.HandlerFunc(removeProductDeviceHandler(productService))))
	router.Handle("GET /v2/products/{productIDOrSlug}/devices/count", requireAuth(authService, http.HandlerFunc(countProductDevicesHandler(productService))))
	router.Handle("GET /v2/products", requireAuth(authService, http.HandlerFunc(listProductsHandler(productService))))
	router.Handle("POST /v2/products", requireAuth(authService, http.HandlerFunc(createProductHandler(productService))))
	router.Handle("GET /v2/products/{productIDOrSlug}", requireAuth(authService, http.HandlerFunc(getProductHandler(productService))))
	router.Handle("PUT /v2/products/{productIDOrSlug}", requireAuth(authService, http.HandlerFunc(updateProductHandler(productService))))
	router.Handle("DELETE /v2/products/{productIDOrSlug}", requireAuth(authService, http.HandlerFunc(deleteProductHandler(productService))))
	router.Handle("GET /v2/products/{productIDOrSlug}/devices", requireAuth(authService, http.HandlerFunc(listProductDevicesHandler(productService))))
	router.Handle("POST /v2/products/{productIDOrSlug}/devices", requireAuth(authService, http.HandlerFunc(addProductDeviceHandler(productService))))
	router.Handle("GET /v2/products/{productIDOrSlug}/devices/{deviceID}", requireAuth(authService, http.HandlerFunc(getProductDeviceHandler(productService))))
	router.Handle("PUT /v2/products/{productIDOrSlug}/devices/{deviceID}", requireAuth(authService, http.HandlerFunc(updateProductDeviceHandler(productService, firmwareService))))
	router.Handle("DELETE /v2/products/{productIDOrSlug}/devices/{deviceID}", requireAuth(authService, http.HandlerFunc(removeProductDeviceHandler(productService))))
	router.Handle("GET /v1/webhooks", requireAuth(authService, http.HandlerFunc(listWebhooksHandler(webhookService))))
	router.Handle("POST /v1/webhooks", requireAuth(authService, http.HandlerFunc(createWebhookHandler(webhookService))))
	router.Handle("GET /v1/webhooks/{webhookID}", requireAuth(authService, http.HandlerFunc(getWebhookHandler(webhookService))))
	router.Handle("PUT /v1/webhooks/{webhookID}", requireAuth(authService, http.HandlerFunc(updateWebhookHandler(webhookService))))
	router.Handle("DELETE /v1/webhooks/{webhookID}", requireAuth(authService, http.HandlerFunc(deleteWebhookHandler(webhookService))))
	router.Handle("GET /v2/webhooks", requireAuth(authService, http.HandlerFunc(listWebhooksHandler(webhookService))))
	router.Handle("POST /v2/webhooks", requireAuth(authService, http.HandlerFunc(createWebhookHandler(webhookService))))
	router.Handle("GET /v2/webhooks/{webhookID}", requireAuth(authService, http.HandlerFunc(getWebhookHandler(webhookService))))
	router.Handle("PUT /v2/webhooks/{webhookID}", requireAuth(authService, http.HandlerFunc(updateWebhookHandler(webhookService))))
	router.Handle("DELETE /v2/webhooks/{webhookID}", requireAuth(authService, http.HandlerFunc(deleteWebhookHandler(webhookService))))

	return requestLogger(logger, router)
}

func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		return err
	}

	listenerAddress := netutil.AdvertisedAddress(listener.Addr())
	s.listenerAddress.Store(listenerAddress)

	s.logger.Info("http listener started", "address", listenerAddress)
	go func() {
		if err := s.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("http server stopped", "error", err)
		}
	}()

	return nil
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
	return s.server.Shutdown(ctx)
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func rootHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"name": "spark-server-go"})
}

func tokenHandler(authService *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		grantType := r.Form.Get("grant_type")
		if grantType != "" && grantType != "password" {
			writeError(w, http.StatusBadRequest, "unsupported_grant_type")
			return
		}

		username := r.Form.Get("username")
		password := r.Form.Get("password")
		if username == "" || password == "" {
			if basicUsername, basicPassword, ok := r.BasicAuth(); ok {
				username = basicUsername
				password = basicPassword
			}
		}

		token, err := authService.Login(r.Context(), username, password)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, repository.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "invalid_grant")
				return
			}
			writeError(w, http.StatusInternalServerError, "server_error")
			return
		}

		expiresIn := int64(time.Until(token.ExpiresAt).Seconds())
		if expiresIn < 0 {
			expiresIn = 0
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": token.Token,
			"token_type":   "bearer",
			"expires_in":   expiresIn,
		})
	}
}

func createUserHandler(authService *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := credentialsFromRequest(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		user, err := authService.CreateUser(r.Context(), username, password)
		if err != nil {
			if errors.Is(err, repository.ErrConflict) {
				writeError(w, http.StatusConflict, "user_exists")
				return
			}
			writeError(w, http.StatusInternalServerError, "server_error")
			return
		}

		writeJSON(w, http.StatusCreated, map[string]any{
			"id":       user.ID,
			"username": user.Username,
		})
	}
}

func listTokensHandler(authService *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		tokens, err := authService.ListTokens(r.Context(), user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "server_error")
			return
		}

		response := make([]map[string]any, 0, len(tokens))
		for _, token := range tokens {
			response = append(response, map[string]any{
				"token":      token.Token,
				"expires_at": token.ExpiresAt,
				"created_at": token.CreatedAt,
			})
		}

		writeJSON(w, http.StatusOK, response)
	}
}

func deleteTokenHandler(authService *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenValue := r.PathValue("token")
		if tokenValue == "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		if err := authService.DeleteToken(r.Context(), tokenValue); err != nil && !errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, "server_error")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, errorCode string) {
	writeJSON(w, status, map[string]string{"error": errorCode})
}

func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}

func credentialsFromRequest(r *http.Request) (string, string, bool) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return "", "", false
		}
		return body.Username, body.Password, body.Username != "" && body.Password != ""
	}

	if err := r.ParseForm(); err != nil {
		return "", "", false
	}

	username := r.Form.Get("username")
	password := r.Form.Get("password")
	return username, password, username != "" && password != ""
}

type contextKey string

const userContextKey contextKey = "user"

func requireAuth(authService *auth.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := authenticateRequest(authService, r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_token")
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func authenticateRequest(authService *auth.Service, r *http.Request) (*domain.User, error) {
	header := r.Header.Get("Authorization")
	tokenValue, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		tokenValue, ok = strings.CutPrefix(header, "bearer ")
	}
	if !ok || tokenValue == "" {
		return nil, repository.ErrNotFound
	}

	user, _, err := authService.AuthenticateToken(r.Context(), tokenValue)
	return user, err
}

func userFromContext(ctx context.Context) *domain.User {
	user, _ := ctx.Value(userContextKey).(*domain.User)
	return user
}
