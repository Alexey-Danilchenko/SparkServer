// Package test verifies auth and token HTTP compatibility.
package test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sparkserver/internal/auth"
	"sparkserver/internal/httpapi"
	filerepo "sparkserver/internal/repository/file"
)

func TestOAuthTokenAndAccessTokenRoutes(t *testing.T) {
	dir := t.TempDir()
	authService := auth.NewService(
		filerepo.NewUserRepository(filepath.Join(dir, "users")),
		filerepo.NewAccessTokenRepository(filepath.Join(dir, "tokens")),
		24*time.Hour,
	)

	if err := authService.EnsureDefaultAdmin(context.Background(), "__admin__", "adminPassword"); err != nil {
		t.Fatalf("ensure default admin: %v", err)
	}

	handler := httpapi.NewHandler(authService, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tokenResponse := postForm(t, handler, "/oauth/token", "grant_type=password&username=__admin__&password=adminPassword")

	if tokenResponse.Code != http.StatusOK {
		t.Fatalf("token status = %d body = %s", tokenResponse.Code, tokenResponse.Body.String())
	}

	var tokenBody struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(tokenResponse.Body).Decode(&tokenBody); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if tokenBody.AccessToken == "" {
		t.Fatal("missing access token")
	}
	if tokenBody.TokenType != "bearer" {
		t.Fatalf("token type = %q", tokenBody.TokenType)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/v1/access_tokens", nil)
	listRequest.Header.Set("Authorization", "Bearer "+tokenBody.AccessToken)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)

	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", listResponse.Code, listResponse.Body.String())
	}

	var tokens []map[string]any
	if err := json.NewDecoder(listResponse.Body).Decode(&tokens); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(tokens) != 1 || tokens[0]["token"] != tokenBody.AccessToken {
		t.Fatalf("tokens = %#v", tokens)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/v1/access_tokens/"+tokenBody.AccessToken, nil)
	deleteRequest.Header.Set("Authorization", "Bearer "+tokenBody.AccessToken)
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)

	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body = %s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestCreateUserAndLogin(t *testing.T) {
	dir := t.TempDir()
	authService := auth.NewService(
		filerepo.NewUserRepository(filepath.Join(dir, "users")),
		filerepo.NewAccessTokenRepository(filepath.Join(dir, "tokens")),
		24*time.Hour,
	)

	handler := httpapi.NewHandler(authService, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	createRequest := httptest.NewRequest(http.MethodPost, "/v1/users", strings.NewReader(`{"username":"alice","password":"secret"}`))
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createRequest)

	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create user status = %d body = %s", createResponse.Code, createResponse.Body.String())
	}

	tokenResponse := postForm(t, handler, "/oauth/token", "grant_type=password&username=alice&password=secret")
	if tokenResponse.Code != http.StatusOK {
		t.Fatalf("token status = %d body = %s", tokenResponse.Code, tokenResponse.Body.String())
	}
}

func TestOAuthRejectsBadPassword(t *testing.T) {
	dir := t.TempDir()
	authService := auth.NewService(
		filerepo.NewUserRepository(filepath.Join(dir, "users")),
		filerepo.NewAccessTokenRepository(filepath.Join(dir, "tokens")),
		24*time.Hour,
	)

	if err := authService.EnsureDefaultAdmin(context.Background(), "__admin__", "adminPassword"); err != nil {
		t.Fatalf("ensure default admin: %v", err)
	}

	handler := httpapi.NewHandler(authService, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	response := postForm(t, handler, "/oauth/token", "grant_type=password&username=__admin__&password=wrong")

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func postForm(
	t       *testing.T,
	handler http.Handler,
	path    string,
	body    string,
) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
