// Package test verifies virtual device provisioning and claiming flows.
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
	"sparkserver/internal/devices"
	"sparkserver/internal/httpapi"
	protocolkeys "sparkserver/internal/protocol/keys"
	filerepo "sparkserver/internal/repository/file"
	"sparkserver/test/collider"
)

func TestColliderGeneratedDeviceRegistersPublicKeyAndGetsClaimed(t *testing.T) {
	dir := t.TempDir()
	authService := auth.NewService(
		filerepo.NewUserRepository(filepath.Join(dir, "users")),
		filerepo.NewAccessTokenRepository(filepath.Join(dir, "tokens")),
		24*time.Hour,
	)
	deviceService := devices.NewService(
		filerepo.NewDeviceRepository(filepath.Join(dir, "devices")),
		filerepo.NewDeviceClaimRepository(filepath.Join(dir, "deviceClaims")),
	)
	keyManager := protocolkeys.NewManager(filepath.Join(dir, "keys"))

	if _, err := authService.CreateUser(context.Background(), "__test__@testaccount.com", "password"); err != nil {
		t.Fatalf("create collider user: %v", err)
	}

	handler := httpapi.NewHandlerWithDeviceKeys(
		authService,
		deviceService,
		nil,
		nil,
		nil,
		nil,
		keyManager,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	tokenResponse := postForm(t, handler, "/oauth/token", "grant_type=password&username=__test__@testaccount.com&password=password")
	if tokenResponse.Code != http.StatusOK {
		t.Fatalf("token status = %d body = %s", tokenResponse.Code, tokenResponse.Body.String())
	}

	var tokenBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(tokenResponse.Body).Decode(&tokenBody); err != nil {
		t.Fatalf("decode token response: %v", err)
	}

	identity := collider.NewIdentity(t)
	provisionBody, err := json.Marshal(map[string]string{
		"deviceID":  identity.DeviceID,
		"publicKey": identity.PublicKeyPEM,
		"filename":  "particle-api",
		"order":     "manual_test",
		"algorithm": "rsa",
	})
	if err != nil {
		t.Fatalf("marshal public key request: %v", err)
	}

	provision := authedRequest(http.MethodPost, "/v1/provisioning/"+identity.DeviceID, string(provisionBody), tokenBody.AccessToken)
	provision.Header.Set("Content-Type", "application/json")
	provisionResponse := httptest.NewRecorder()
	handler.ServeHTTP(provisionResponse, provision)
	if provisionResponse.Code != http.StatusOK {
		t.Fatalf("provision status = %d body = %s", provisionResponse.Code, provisionResponse.Body.String())
	}

	var provisioned map[string]any
	if err := json.NewDecoder(provisionResponse.Body).Decode(&provisioned); err != nil {
		t.Fatalf("decode provision response: %v", err)
	}
	if provisioned["id"] != identity.DeviceID {
		t.Fatalf("provisioned device = %#v", provisioned)
	}

	claimedDevice, err := deviceService.Get(context.Background(), "__test__@testaccount.com", identity.DeviceID)
	if err != nil {
		t.Fatalf("get claimed device: %v", err)
	}
	if claimedDevice.OwnerID != "__test__@testaccount.com" {
		t.Fatalf("owner id = %q", claimedDevice.OwnerID)
	}

	savedPublicKey, err := keyManager.LoadDevicePublicKey(identity.DeviceID)
	if err != nil {
		t.Fatalf("load registered device key: %v", err)
	}
	if savedPublicKey.N.Cmp(identity.PrivateKey.PublicKey.N) != 0 {
		t.Fatal("registered public key does not match generated device key")
	}

	list := authedRequest(http.MethodGet, "/v1/devices", "", tokenBody.AccessToken)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", listResponse.Code, listResponse.Body.String())
	}
	if !strings.Contains(listResponse.Body.String(), identity.DeviceID) {
		t.Fatalf("list body = %s", listResponse.Body.String())
	}
}
