// Package test verifies product and product-device HTTP routes.
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
	"sparkserver/internal/domain"
	"sparkserver/internal/events"
	"sparkserver/internal/firmware"
	"sparkserver/internal/httpapi"
	"sparkserver/internal/products"
	filerepo "sparkserver/internal/repository/file"
)

func TestProductRoutesCRUD(t *testing.T) {
	handler, token := newAuthenticatedProductHandler(t)

	create := authedRequest(http.MethodPost, "/v1/products", `{"id":"product-1","slug":"brew-controller","name":"Brew Controller","description":"Controls brews"}`, token)
	create.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", createResponse.Code, createResponse.Body.String())
	}

	var product map[string]any
	if err := json.NewDecoder(createResponse.Body).Decode(&product); err != nil {
		t.Fatalf("decode product: %v", err)
	}
	if product["id"] != "product-1" || product["slug"] != "brew-controller" || product["name"] != "Brew Controller" {
		t.Fatalf("product = %#v", product)
	}

	list := authedRequest(http.MethodGet, "/v2/products", "", token)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", listResponse.Code, listResponse.Body.String())
	}
	var products []map[string]any
	if err := json.NewDecoder(listResponse.Body).Decode(&products); err != nil {
		t.Fatalf("decode products: %v", err)
	}
	if len(products) != 1 || products[0]["id"] != "product-1" {
		t.Fatalf("products = %#v", products)
	}

	update := authedRequest(http.MethodPut, "/v2/products/brew-controller", `{"name":"Brew Controller Pro","slug":"brew-pro"}`, token)
	update.Header.Set("Content-Type", "application/json")
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, update)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update status = %d body = %s", updateResponse.Code, updateResponse.Body.String())
	}
	var updated map[string]any
	if err := json.NewDecoder(updateResponse.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated product: %v", err)
	}
	if updated["name"] != "Brew Controller Pro" || updated["slug"] != "brew-pro" {
		t.Fatalf("updated = %#v", updated)
	}

	get := authedRequest(http.MethodGet, "/v1/products/brew-pro", "", token)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status = %d body = %s", getResponse.Code, getResponse.Body.String())
	}

	remove := authedRequest(http.MethodDelete, "/v1/products/product-1", "", token)
	removeResponse := httptest.NewRecorder()
	handler.ServeHTTP(removeResponse, remove)
	if removeResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body = %s", removeResponse.Code, removeResponse.Body.String())
	}
}

func TestProductDeviceAssociationRoutes(t *testing.T) {
	handler, token := newAuthenticatedProductHandler(t)

	product := authedRequest(http.MethodPost, "/v1/products", `{"id":"product-1","slug":"brew-controller","name":"Brew Controller"}`, token)
	product.Header.Set("Content-Type", "application/json")
	productResponse := httptest.NewRecorder()
	handler.ServeHTTP(productResponse, product)
	if productResponse.Code != http.StatusCreated {
		t.Fatalf("create product status = %d body = %s", productResponse.Code, productResponse.Body.String())
	}

	claim := authedRequest(http.MethodPost, "/v1/devices", `{"id":"device-1"}`, token)
	claim.Header.Set("Content-Type", "application/json")
	claimResponse := httptest.NewRecorder()
	handler.ServeHTTP(claimResponse, claim)
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("claim status = %d body = %s", claimResponse.Code, claimResponse.Body.String())
	}

	add := authedRequest(http.MethodPost, "/v2/products/brew-controller/devices", `{"device_id":"device-1"}`, token)
	add.Header.Set("Content-Type", "application/json")
	addResponse := httptest.NewRecorder()
	handler.ServeHTTP(addResponse, add)
	if addResponse.Code != http.StatusCreated {
		t.Fatalf("add status = %d body = %s", addResponse.Code, addResponse.Body.String())
	}
	var link map[string]any
	if err := json.NewDecoder(addResponse.Body).Decode(&link); err != nil {
		t.Fatalf("decode link: %v", err)
	}
	if link["product_id"] != "product-1" || link["device_id"] != "device-1" {
		t.Fatalf("link = %#v", link)
	}

	list := authedRequest(http.MethodGet, "/v1/products/product-1/devices", "", token)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", listResponse.Code, listResponse.Body.String())
	}
	var productDevices []map[string]any
	if err := json.NewDecoder(listResponse.Body).Decode(&productDevices); err != nil {
		t.Fatalf("decode product devices: %v", err)
	}
	if len(productDevices) != 1 || productDevices[0]["id"] != "device-1" || productDevices[0]["product_id"] != "product-1" {
		t.Fatalf("product devices = %#v", productDevices)
	}

	remove := authedRequest(http.MethodDelete, "/v2/products/product-1/devices/device-1", "", token)
	removeResponse := httptest.NewRecorder()
	handler.ServeHTTP(removeResponse, remove)
	if removeResponse.Code != http.StatusNoContent {
		t.Fatalf("remove status = %d body = %s", removeResponse.Code, removeResponse.Body.String())
	}
}

func TestProductCompatibilityRoutes(t *testing.T) {
	handler, token, eventService := newAuthenticatedProductHandlerWithEvents(t)

	product := authedRequest(http.MethodPost, "/v1/products", `{"id":"product-1","slug":"brew-controller","name":"Brew Controller"}`, token)
	product.Header.Set("Content-Type", "application/json")
	productResponse := httptest.NewRecorder()
	handler.ServeHTTP(productResponse, product)
	if productResponse.Code != http.StatusCreated {
		t.Fatalf("create product status = %d body = %s", productResponse.Code, productResponse.Body.String())
	}

	claim := authedRequest(http.MethodPost, "/v1/devices", `{"id":"device-1"}`, token)
	claim.Header.Set("Content-Type", "application/json")
	claimResponse := httptest.NewRecorder()
	handler.ServeHTTP(claimResponse, claim)
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("claim status = %d body = %s", claimResponse.Code, claimResponse.Body.String())
	}

	add := authedRequest(http.MethodPost, "/v1/products/product-1/devices", `{"device_id":"device-1"}`, token)
	add.Header.Set("Content-Type", "application/json")
	addResponse := httptest.NewRecorder()
	handler.ServeHTTP(addResponse, add)
	if addResponse.Code != http.StatusCreated {
		t.Fatalf("add status = %d body = %s", addResponse.Code, addResponse.Body.String())
	}

	assertJSONNumber(t, handler, token, "/v2/products/count", 1)
	assertJSONNumber(t, handler, token, "/v2/products/product-1/devices/count", 1)

	config := authedRequest(http.MethodGet, "/v1/products/product-1/config", "", token)
	configResponse := httptest.NewRecorder()
	handler.ServeHTTP(configResponse, config)
	if configResponse.Code != http.StatusOK {
		t.Fatalf("config status = %d body = %s", configResponse.Code, configResponse.Body.String())
	}
	var configBody map[string]map[string]any
	if err := json.NewDecoder(configResponse.Body).Decode(&configBody); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if configBody["product_configuration"]["product_id"] != "product-1" {
		t.Fatalf("config = %#v", configBody)
	}

	update := authedRequest(http.MethodPut, "/v1/products/product-1/devices/device-1", `{"notes":"bench device","quarantined":true,"desired_firmware_version":7}`, token)
	update.Header.Set("Content-Type", "application/json")
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, update)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update product device status = %d body = %s", updateResponse.Code, updateResponse.Body.String())
	}
	var updatedLink map[string]any
	if err := json.NewDecoder(updateResponse.Body).Decode(&updatedLink); err != nil {
		t.Fatalf("decode product device update: %v", err)
	}
	if updatedLink["notes"] != "bench device" || updatedLink["quarantined"] != true || updatedLink["desired_firmware_version"] != float64(7) {
		t.Fatalf("updated link = %#v", updatedLink)
	}

	getDevice := authedRequest(http.MethodGet, "/v2/products/product-1/devices/device-1", "", token)
	getDeviceResponse := httptest.NewRecorder()
	handler.ServeHTTP(getDeviceResponse, getDevice)
	if getDeviceResponse.Code != http.StatusOK {
		t.Fatalf("get product device status = %d body = %s", getDeviceResponse.Code, getDeviceResponse.Body.String())
	}
	var productDevice map[string]any
	if err := json.NewDecoder(getDeviceResponse.Body).Decode(&productDevice); err != nil {
		t.Fatalf("decode product device: %v", err)
	}
	if productDevice["id"] != "device-1" || productDevice["product_id"] != "product-1" || productDevice["notes"] != "bench device" {
		t.Fatalf("product device = %#v", productDevice)
	}

	assertUnsupportedProductFeature(t, handler, token, http.MethodPost, "/v1/products/product-1/clients")
	assertUnsupportedProductFeature(t, handler, token, http.MethodDelete, "/v1/products/product-1/team/alice")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	streamRequest := httptest.NewRequest(http.MethodGet, "/v1/products/product-1/events/spark/flash", nil).WithContext(ctx)
	streamRequest.Header.Set("Authorization", "Bearer "+token)
	streamResponse := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(streamResponse, streamRequest)
		close(done)
	}()
	time.Sleep(25 * time.Millisecond)

	if _, err := eventService.Publish(context.Background(), &domain.Event{Name: "spark/flash/completed", ProductID: "product-1", DeviceID: "device-1"}); err != nil {
		t.Fatalf("publish product event: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		if strings.Contains(streamResponse.Body.String(), "event: spark/flash/completed") {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for product event, body = %s", streamResponse.Body.String())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("product event stream did not stop after cancel")
	}
}

func newAuthenticatedProductHandler(t *testing.T) (http.Handler, string) {
	t.Helper()
	handler, token, _ := newAuthenticatedProductHandlerWithEvents(t)
	return handler, token
}

func newAuthenticatedProductHandlerWithEvents(
	t *testing.T,
) (http.Handler, string, *events.Service) {
	t.Helper()

	dir := t.TempDir()
	authService := auth.NewService(
		filerepo.NewUserRepository(filepath.Join(dir, "users")),
		filerepo.NewAccessTokenRepository(filepath.Join(dir, "tokens")),
		24*time.Hour,
	)
	deviceRepository := filerepo.NewDeviceRepository(filepath.Join(dir, "devices"))
	deviceService := devices.NewService(
		deviceRepository,
		filerepo.NewDeviceClaimRepository(filepath.Join(dir, "deviceClaims")),
	)
	firmwareService := firmware.NewService(
		filerepo.NewProductFirmwareRepository(filepath.Join(dir, "firmware", "metadata")),
		filepath.Join(dir, "firmware", "binaries"),
		filerepo.NewFlashJobRepository(filepath.Join(dir, "firmware", "flashJobs")),
	)
	eventService := events.NewService(filerepo.NewEventRepository(filepath.Join(dir, "events")))
	productService := products.NewService(
		filerepo.NewProductRepository(filepath.Join(dir, "products")),
		filerepo.NewProductDeviceRepository(filepath.Join(dir, "products", "devices")),
		deviceRepository,
	)

	if err := authService.EnsureDefaultAdmin(context.Background(), "__admin__", "adminPassword"); err != nil {
		t.Fatalf("ensure default admin: %v", err)
	}

	handler := httpapi.NewHandlerWithFirmwareAndProducts(
		authService,
		deviceService,
		eventService,
		firmwareService,
		productService,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
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
	return handler, body.AccessToken, eventService
}

func assertJSONNumber(
	t        *testing.T,
	handler  http.Handler,
	token    string,
	path     string,
	expected float64,
) {
	t.Helper()

	request := authedRequest(http.MethodGet, path, "", token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("%s status = %d body = %s", path, response.Code, response.Body.String())
	}
	var count float64
	if err := json.NewDecoder(response.Body).Decode(&count); err != nil {
		t.Fatalf("decode %s count: %v", path, err)
	}
	if count != expected {
		t.Fatalf("%s count = %v want %v", path, count, expected)
	}
}

func assertUnsupportedProductFeature(
	t       *testing.T,
	handler http.Handler,
	token   string,
	method  string,
	path    string,
) {
	t.Helper()

	request := authedRequest(method, path, "", token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotImplemented {
		t.Fatalf("%s %s status = %d body = %s", method, path, response.Code, response.Body.String())
	}
}
