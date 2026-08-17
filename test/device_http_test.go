// Package test verifies device HTTP route compatibility.
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
	jsonfile "sparkserver/internal/jsonfile"
)

func TestDeviceRoutes(t *testing.T) {
	handler, token := newAuthenticatedDeviceHandler(t)

	claim := authedRequest(http.MethodPost, "/v1/devices", `{"id":"device-1"}`, token)
	claim.Header.Set("Content-Type", "application/json")
	claimResponse := httptest.NewRecorder()
	handler.ServeHTTP(claimResponse, claim)

	if claimResponse.Code != http.StatusOK {
		t.Fatalf("claim status = %d body = %s", claimResponse.Code, claimResponse.Body.String())
	}

	var claimed map[string]any
	if err := json.NewDecoder(claimResponse.Body).Decode(&claimed); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if claimed["id"] != "device-1" {
		t.Fatalf("claimed device = %#v", claimed)
	}

	rename := authedRequest(http.MethodPut, "/v1/devices/device-1", `{"name":"kettle"}`, token)
	rename.Header.Set("Content-Type", "application/json")
	renameResponse := httptest.NewRecorder()
	handler.ServeHTTP(renameResponse, rename)

	if renameResponse.Code != http.StatusOK {
		t.Fatalf("rename status = %d body = %s", renameResponse.Code, renameResponse.Body.String())
	}

	getByName := authedRequest(http.MethodGet, "/v1/devices/kettle", "", token)
	getByNameResponse := httptest.NewRecorder()
	handler.ServeHTTP(getByNameResponse, getByName)

	if getByNameResponse.Code != http.StatusOK {
		t.Fatalf("get by name status = %d body = %s", getByNameResponse.Code, getByNameResponse.Body.String())
	}

	var device map[string]any
	if err := json.NewDecoder(getByNameResponse.Body).Decode(&device); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if device["id"] != "device-1" || device["name"] != "kettle" {
		t.Fatalf("device = %#v", device)
	}

	list := authedRequest(http.MethodGet, "/v1/devices", "", token)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)

	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", listResponse.Code, listResponse.Body.String())
	}

	var devices []map[string]any
	if err := json.NewDecoder(listResponse.Body).Decode(&devices); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(devices) != 1 || devices[0]["name"] != "kettle" {
		t.Fatalf("devices = %#v", devices)
	}

	ping := authedRequest(http.MethodPut, "/v1/devices/kettle/ping", "", token)
	pingResponse := httptest.NewRecorder()
	handler.ServeHTTP(pingResponse, ping)

	if pingResponse.Code != http.StatusOK {
		t.Fatalf("ping status = %d body = %s", pingResponse.Code, pingResponse.Body.String())
	}

	remove := authedRequest(http.MethodDelete, "/v1/devices/kettle", "", token)
	removeResponse := httptest.NewRecorder()
	handler.ServeHTTP(removeResponse, remove)

	if removeResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body = %s", removeResponse.Code, removeResponse.Body.String())
	}

	afterDelete := authedRequest(http.MethodGet, "/v1/devices/kettle", "", token)
	afterDeleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(afterDeleteResponse, afterDelete)

	if afterDeleteResponse.Code != http.StatusNotFound {
		t.Fatalf("after delete status = %d body = %s", afterDeleteResponse.Code, afterDeleteResponse.Body.String())
	}
}

func TestDeviceRoutesRequireAuth(t *testing.T) {
	handler, _ := newAuthenticatedDeviceHandler(t)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/devices", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestDeviceClaimAndProvisioningRoutes(t *testing.T) {
	handler, token := newAuthenticatedDeviceHandler(t)

	claimRequest := authedRequest(http.MethodPost, "/v1/device_claims", "", token)
	claimResponse := httptest.NewRecorder()
	handler.ServeHTTP(claimResponse, claimRequest)

	if claimResponse.Code != http.StatusOK {
		t.Fatalf("claim code status = %d body = %s", claimResponse.Code, claimResponse.Body.String())
	}

	var claimBody struct {
		ClaimCode string `json:"claim_code"`
	}
	if err := json.NewDecoder(claimResponse.Body).Decode(&claimBody); err != nil {
		t.Fatalf("decode claim code response: %v", err)
	}
	if claimBody.ClaimCode == "" {
		t.Fatal("missing claim code")
	}

	provisionRequest := httptest.NewRequest(http.MethodPost, "/v1/provisioning/device-2", strings.NewReader(`{"claim_code":"`+claimBody.ClaimCode+`"}`))
	provisionRequest.Header.Set("Content-Type", "application/json")
	provisionResponse := httptest.NewRecorder()
	handler.ServeHTTP(provisionResponse, provisionRequest)

	if provisionResponse.Code != http.StatusOK {
		t.Fatalf("provision status = %d body = %s", provisionResponse.Code, provisionResponse.Body.String())
	}

	var device map[string]any
	if err := json.NewDecoder(provisionResponse.Body).Decode(&device); err != nil {
		t.Fatalf("decode provision response: %v", err)
	}
	if device["id"] != "device-2" {
		t.Fatalf("provisioned device = %#v", device)
	}

	reuseRequest := httptest.NewRequest(http.MethodPost, "/v1/provisioning/device-3", strings.NewReader(`{"claim_code":"`+claimBody.ClaimCode+`"}`))
	reuseRequest.Header.Set("Content-Type", "application/json")
	reuseResponse := httptest.NewRecorder()
	handler.ServeHTTP(reuseResponse, reuseRequest)

	if reuseResponse.Code != http.StatusNotFound {
		t.Fatalf("reuse status = %d body = %s", reuseResponse.Code, reuseResponse.Body.String())
	}
}

func TestDeviceVariableAndFunctionRoutesUseLiveClient(t *testing.T) {
	handler, token, deviceService, liveClient := newAuthenticatedDeviceHandlerWithService(t)
	liveClient.variables["device-1:temperature"] = "21.5"
	liveClient.functionReturns["device-1:brew"] = 7

	claim := authedRequest(http.MethodPost, "/v1/devices", `{"id":"device-1"}`, token)
	claim.Header.Set("Content-Type", "application/json")
	claimResponse := httptest.NewRecorder()
	handler.ServeHTTP(claimResponse, claim)
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("claim status = %d body = %s", claimResponse.Code, claimResponse.Body.String())
	}
	if err := deviceService.MarkConnected(context.Background(), "device-1"); err != nil {
		t.Fatalf("mark connected: %v", err)
	}

	variableRequest := authedRequest(http.MethodGet, "/v1/devices/device-1/temperature", "", token)
	variableResponse := httptest.NewRecorder()
	handler.ServeHTTP(variableResponse, variableRequest)
	if variableResponse.Code != http.StatusOK {
		t.Fatalf("variable status = %d body = %s", variableResponse.Code, variableResponse.Body.String())
	}

	var variableBody map[string]any
	if err := json.NewDecoder(variableResponse.Body).Decode(&variableBody); err != nil {
		t.Fatalf("decode variable response: %v", err)
	}
	if variableBody["name"] != "temperature" || variableBody["result"] != "21.5" {
		t.Fatalf("variable body = %#v", variableBody)
	}

	functionRequest := authedRequest(http.MethodPost, "/v1/devices/device-1/brew", `arg=start`, token)
	functionRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	functionResponse := httptest.NewRecorder()
	handler.ServeHTTP(functionResponse, functionRequest)
	if functionResponse.Code != http.StatusOK {
		t.Fatalf("function status = %d body = %s", functionResponse.Code, functionResponse.Body.String())
	}

	var functionBody map[string]any
	if err := json.NewDecoder(functionResponse.Body).Decode(&functionBody); err != nil {
		t.Fatalf("decode function response: %v", err)
	}
	if functionBody["name"] != "brew" || functionBody["return_value"] != float64(7) {
		t.Fatalf("function body = %#v", functionBody)
	}
	if liveClient.lastArgument != "start" {
		t.Fatalf("function argument = %q", liveClient.lastArgument)
	}
}

func TestDeviceVariableRouteTimesOut(t *testing.T) {
	handler, token, deviceService, liveClient := newAuthenticatedDeviceHandlerWithService(t, devices.WithAPITimeout(5*time.Millisecond))
	liveClient.blockUntilDone = true

	claim := authedRequest(http.MethodPost, "/v1/devices", `{"id":"device-1"}`, token)
	claim.Header.Set("Content-Type", "application/json")
	claimResponse := httptest.NewRecorder()
	handler.ServeHTTP(claimResponse, claim)
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("claim status = %d body = %s", claimResponse.Code, claimResponse.Body.String())
	}
	if err := deviceService.MarkConnected(context.Background(), "device-1"); err != nil {
		t.Fatalf("mark connected: %v", err)
	}

	variableRequest := authedRequest(http.MethodGet, "/v1/devices/device-1/temperature", "", token)
	variableResponse := httptest.NewRecorder()
	handler.ServeHTTP(variableResponse, variableRequest)
	if variableResponse.Code != http.StatusRequestTimeout {
		t.Fatalf("variable status = %d body = %s", variableResponse.Code, variableResponse.Body.String())
	}
	if !strings.Contains(variableResponse.Body.String(), "device_timeout") {
		t.Fatalf("variable body = %s", variableResponse.Body.String())
	}
}

func newAuthenticatedDeviceHandler(t *testing.T) (http.Handler, string) {
	t.Helper()

	handler, token, _, _ := newAuthenticatedDeviceHandlerWithService(t)
	return handler, token
}

func newAuthenticatedDeviceHandlerWithService(
	t *testing.T,
	options ...devices.Option,
) (http.Handler, string, *devices.Service, *mockLiveDeviceClient) {
	t.Helper()

	dir := t.TempDir()
	authService := auth.NewService(
		jsonfile.NewUserRepository(filepath.Join(dir, "users")),
		jsonfile.NewAccessTokenRepository(filepath.Join(dir, "tokens")),
		24*time.Hour,
	)
	deviceService := devices.NewService(
		jsonfile.NewDeviceRepository(filepath.Join(dir, "devices")),
		jsonfile.NewDeviceClaimRepository(filepath.Join(dir, "deviceClaims")),
		options...,
	)
	liveClient := &mockLiveDeviceClient{
		variables:       make(map[string]string),
		functionReturns: make(map[string]int),
	}
	deviceService.SetLiveClient(liveClient)

	if err := authService.EnsureDefaultAdmin(context.Background(), "__admin__", "adminPassword"); err != nil {
		t.Fatalf("ensure default admin: %v", err)
	}

	handler := httpapi.NewHandler(httpapi.Dependencies{Auth: authService, Devices: deviceService}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	response := postForm(t, handler, "/oauth/token", "grant_type=password&username=__admin__&password=adminPassword")
	if response.Code != http.StatusOK {
		t.Fatalf("token status = %d body = %s", response.Code, response.Body.String())
	}

	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode token response: %v", err)
	}

	return handler, body.AccessToken, deviceService, liveClient
}

func authedRequest(method string, path string, body string, token string) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}

type mockLiveDeviceClient struct {
	variables       map[string]string
	functionReturns map[string]int
	lastArgument    string
	blockUntilDone  bool
}

func (client *mockLiveDeviceClient) GetVariable(
	ctx context.Context,
	deviceID string,
	variableName string,
) (string, error) {
	if client.blockUntilDone {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return client.variables[deviceID+":"+variableName], nil
}

func (client *mockLiveDeviceClient) CallFunction(
	ctx context.Context,
	deviceID string,
	functionName string,
	argument string,
) (int, error) {
	if client.blockUntilDone {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	client.lastArgument = argument
	return client.functionReturns[deviceID+":"+functionName], nil
}

func (client *mockLiveDeviceClient) Ping(ctx context.Context, _ string) error {
	if client.blockUntilDone {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
