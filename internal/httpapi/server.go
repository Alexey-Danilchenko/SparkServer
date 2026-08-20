// Package httpapi exposes Spark/Particle-compatible REST and SSE routes.
package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"sparkserver/internal/auth"
	"sparkserver/internal/devices"
	"sparkserver/internal/events"
	"sparkserver/internal/netutil"
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
	tls             TLSConfig
	runtimeErrors   chan error
}

// TLSConfig controls HTTPS for the HTTP API listener.
type TLSConfig struct {
	Enabled         bool
	CertificateFile string
	PrivateKeyFile  string
}

// Dependencies contains the services used by the HTTP API route tree.
type Dependencies struct {
	Auth       *auth.Service
	Devices    *devices.Service
	Events     *events.Service
	Firmware   FirmwareService
	Products   ProductService
	Webhooks   WebhookService
	DeviceKeys DeviceKeyRegistrar
}

// Timeouts controls request intake and keep-alive behavior. WriteTimeout is
// intentionally omitted because the API serves long-lived SSE responses.
type Timeouts struct {
	ReadHeader time.Duration
	Read       time.Duration
	Idle       time.Duration
}

// Option configures optional HTTP server behavior.
type Option func(*Server)

// WithTLS enables HTTPS with the supplied certificate configuration.
func WithTLS(config TLSConfig) Option {
	return func(server *Server) {
		server.tls = config
	}
}

// WithTimeouts overrides the production-safe HTTP timeout defaults.
func WithTimeouts(timeouts Timeouts) Option {
	return func(server *Server) {
		server.server.ReadHeaderTimeout = timeouts.ReadHeader
		server.server.ReadTimeout = timeouts.Read
		server.server.IdleTimeout = timeouts.Idle
	}
}

// New builds the HTTP API with explicit dependencies and optional transport settings.
func New(address string, dependencies Dependencies, logger *slog.Logger, options ...Option) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	server := &Server{
		address:       address,
		logger:        logger,
		runtimeErrors: make(chan error, 1),
		server: &http.Server{
			Addr:              address,
			Handler:           NewHandler(dependencies, logger),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			IdleTimeout:       120 * time.Second,
		},
	}
	for _, option := range options {
		option(server)
	}
	return server
}

// NewHandler registers the compatibility routes onto a ServeMux.
func NewHandler(dependencies Dependencies, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	authService := dependencies.Auth
	deviceService := dependencies.Devices
	eventService := dependencies.Events
	firmwareService := dependencies.Firmware
	productService := dependencies.Products
	webhookService := dependencies.Webhooks
	keyRegistrar := dependencies.DeviceKeys

	router := http.NewServeMux()
	registerCoreRoutes(router, authService)
	registerDeviceRoutes(router, authService, deviceService, firmwareService, keyRegistrar)
	registerEventRoutes(router, authService, eventService, productService)
	registerFirmwareRoutes(router, authService, firmwareService)
	registerProductRoutes(router, authService, productService, firmwareService)
	registerWebhookRoutes(router, authService, webhookService)

	return requestLogger(logger, router)
}

func registerCoreRoutes(router *http.ServeMux, authService *auth.Service) {
	router.HandleFunc("GET /health", healthHandler)
	router.HandleFunc("GET /{$}", rootHandler)
	router.HandleFunc("POST /oauth/token", tokenHandler(authService))
	router.HandleFunc("POST /v1/ping", pingHandler)
	router.HandleFunc("POST /v1/users", createUserHandler(authService))
	router.Handle("GET /v1/access_tokens", requireAuth(authService, http.HandlerFunc(listTokensHandler(authService))))
	router.Handle("DELETE /v1/access_tokens/{token}", requireAuth(authService, http.HandlerFunc(deleteTokenHandler(authService))))
}

func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("listen for HTTP API on %s: %w", s.address, err)
	}

	serverListener, err := s.serverListener(listener)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("configure HTTP API listener: %w", err)
	}

	listenerAddress := netutil.AdvertisedAddress(listener.Addr())
	s.listenerAddress.Store(listenerAddress)

	message := "http listener started"
	if s.tls.Enabled {
		message = "https listener started"
	}
	s.logger.Info(message, "address", listenerAddress)
	go func() {
		if err := s.server.Serve(serverListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.runtimeErrors <- fmt.Errorf("serve HTTP API: %w", err)
		}
	}()

	return nil
}

// Errors reports terminal runtime failures from the HTTP listener.
func (s *Server) Errors() <-chan error {
	return s.runtimeErrors
}

func (s *Server) serverListener(listener net.Listener) (net.Listener, error) {
	if !s.tls.Enabled {
		return listener, nil
	}
	if s.tls.CertificateFile == "" {
		return nil, errors.New("SSL_CERTIFICATE_FILEPATH is required when USE_SSL is true")
	}
	if s.tls.PrivateKeyFile == "" {
		return nil, errors.New("SSL_PRIVATE_KEY_FILEPATH is required when USE_SSL is true")
	}

	certificate, err := tls.LoadX509KeyPair(s.tls.CertificateFile, s.tls.PrivateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load HTTPS certificate: %w", err)
	}

	return tls.NewListener(listener, &tls.Config{
		Certificates: []tls.Certificate{certificate},
	}), nil
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
			if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "invalid_grant")
				return
			}
			writeError(w, http.StatusInternalServerError, "server_error")
			return
		}

		expiresIn := max(int64(time.Until(token.ExpiresAt).Seconds()), 0)

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
			if errors.Is(err, auth.ErrConflict) {
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

		if err := authService.DeleteToken(r.Context(), tokenValue); err != nil && !errors.Is(err, auth.ErrNotFound) {
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

func authenticateRequest(authService *auth.Service, r *http.Request) (*auth.User, error) {
	header := r.Header.Get("Authorization")
	tokenValue, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		tokenValue, ok = strings.CutPrefix(header, "bearer ")
	}
	if !ok || tokenValue == "" {
		return nil, auth.ErrNotFound
	}

	user, _, err := authService.AuthenticateToken(r.Context(), tokenValue)
	return user, err
}

func userFromContext(ctx context.Context) *auth.User {
	user, _ := ctx.Value(userContextKey).(*auth.User)
	return user
}
