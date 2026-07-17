// Package test verifies firmware metadata and OTA HTTP routes.
package test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sparkserver/internal/auth"
	"sparkserver/internal/devices"
	"sparkserver/internal/firmware"
	"sparkserver/internal/httpapi"
	filerepo "sparkserver/internal/repository/file"
)

func TestProductFirmwareUploadAndListRoutes(t *testing.T) {
	handler, token := newAuthenticatedFirmwareHandler(t)
	firmwareBytes := []byte{0x01, 0x02, 0x03, 0x04}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("version", "7"); err != nil {
		t.Fatalf("write version field: %v", err)
	}
	if err := writer.WriteField("title", "Brew Controller 7"); err != nil {
		t.Fatalf("write title field: %v", err)
	}
	if err := writer.WriteField("current", "true"); err != nil {
		t.Fatalf("write current field: %v", err)
	}
	file, err := writer.CreateFormFile("firmware", "brew.bin")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := file.Write(firmwareBytes); err != nil {
		t.Fatalf("write firmware: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request := authedRequest(http.MethodPost, "/v1/products/brew-controller/firmware", body.String(), token)
	request.Body = io.NopCloser(bytes.NewReader(body.Bytes()))
	request.ContentLength = int64(body.Len())
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("upload status = %d body = %s", response.Code, response.Body.String())
	}

	var upload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&upload); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if upload["product_id"] != "brew-controller" || upload["version"] != float64(7) || upload["filename"] != "brew.bin" {
		t.Fatalf("upload response = %#v", upload)
	}
	expectedHash := sha256.Sum256(firmwareBytes)
	if upload["sha256"] != hex.EncodeToString(expectedHash[:]) {
		t.Fatalf("sha256 = %#v", upload["sha256"])
	}
	binaryPath, ok := upload["binary_path"].(string)
	if !ok || binaryPath == "" {
		t.Fatalf("binary path = %#v", upload["binary_path"])
	}
	if _, err := os.Stat(binaryPath); err != nil {
		t.Fatalf("stat binary path: %v", err)
	}

	list := authedRequest(http.MethodGet, "/v2/products/brew-controller/firmwares", "", token)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", listResponse.Code, listResponse.Body.String())
	}

	var firmwares []map[string]any
	if err := json.NewDecoder(listResponse.Body).Decode(&firmwares); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(firmwares) != 1 || firmwares[0]["id"] != upload["id"] || firmwares[0]["current"] != true {
		t.Fatalf("firmwares = %#v", firmwares)
	}
}

func TestProductFirmwareRawUploadRoute(t *testing.T) {
	handler, token := newAuthenticatedFirmwareHandler(t)

	request := authedRequest(http.MethodPost, "/v2/products/product-1/firmwares?filename=raw.bin", "\x01\x02", token)
	request.Header.Set("Content-Type", "application/octet-stream")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("raw upload status = %d body = %s", response.Code, response.Body.String())
	}

	var upload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&upload); err != nil {
		t.Fatalf("decode raw upload response: %v", err)
	}
	if upload["filename"] != "raw.bin" || upload["content_type"] != "application/octet-stream" {
		t.Fatalf("raw upload response = %#v", upload)
	}
}

func TestProductFirmwareUpdateAndDeleteRoutes(t *testing.T) {
	handler, token := newAuthenticatedFirmwareHandler(t)

	upload := authedRequest(http.MethodPost, "/v2/products/product-1/firmwares?filename=raw.bin&version=1", "\x01\x02", token)
	upload.Header.Set("Content-Type", "application/octet-stream")
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, upload)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload status = %d body = %s", uploadResponse.Code, uploadResponse.Body.String())
	}

	var created map[string]any
	if err := json.NewDecoder(uploadResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	firmwareID := created["id"].(string)
	oldBinaryPath := created["binary_path"].(string)

	metadataUpdate := authedRequest(
		http.MethodPut,
		"/v1/products/product-1/firmware/"+firmwareID,
		`{"title":"Updated","description":"Updated metadata","version":3,"released":true}`,
		token,
	)
	metadataUpdate.Header.Set("Content-Type", "application/json")
	metadataResponse := httptest.NewRecorder()
	handler.ServeHTTP(metadataResponse, metadataUpdate)
	if metadataResponse.Code != http.StatusOK {
		t.Fatalf("metadata update status = %d body = %s", metadataResponse.Code, metadataResponse.Body.String())
	}

	var updatedMetadata map[string]any
	if err := json.NewDecoder(metadataResponse.Body).Decode(&updatedMetadata); err != nil {
		t.Fatalf("decode metadata update: %v", err)
	}
	if updatedMetadata["title"] != "Updated" || updatedMetadata["version"] != float64(3) || updatedMetadata["released"] != true {
		t.Fatalf("metadata update = %#v", updatedMetadata)
	}

	replacement := []byte{0x09, 0x08, 0x07}
	binaryUpdate := authedRequest(
		http.MethodPut,
		"/v2/products/product-1/firmwares/"+firmwareID+"?filename=replacement.ota&current=true",
		string(replacement),
		token,
	)
	binaryUpdate.Header.Set("Content-Type", "application/octet-stream")
	binaryResponse := httptest.NewRecorder()
	handler.ServeHTTP(binaryResponse, binaryUpdate)
	if binaryResponse.Code != http.StatusOK {
		t.Fatalf("binary update status = %d body = %s", binaryResponse.Code, binaryResponse.Body.String())
	}

	var updatedBinary map[string]any
	if err := json.NewDecoder(binaryResponse.Body).Decode(&updatedBinary); err != nil {
		t.Fatalf("decode binary update: %v", err)
	}
	expectedHash := sha256.Sum256(replacement)
	if updatedBinary["filename"] != "replacement.ota" ||
		updatedBinary["size"] != float64(len(replacement)) ||
		updatedBinary["sha256"] != hex.EncodeToString(expectedHash[:]) ||
		updatedBinary["current"] != true {
		t.Fatalf("binary update = %#v", updatedBinary)
	}
	if _, err := os.Stat(oldBinaryPath); !os.IsNotExist(err) {
		t.Fatalf("old binary path still exists or stat failed unexpectedly: %v", err)
	}
	newBinaryPath := updatedBinary["binary_path"].(string)
	if _, err := os.Stat(newBinaryPath); err != nil {
		t.Fatalf("stat replacement binary: %v", err)
	}

	remove := authedRequest(http.MethodDelete, "/v1/products/product-1/firmware/"+firmwareID, "", token)
	removeResponse := httptest.NewRecorder()
	handler.ServeHTTP(removeResponse, remove)
	if removeResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body = %s", removeResponse.Code, removeResponse.Body.String())
	}
	if _, err := os.Stat(newBinaryPath); !os.IsNotExist(err) {
		t.Fatalf("replacement binary still exists or stat failed unexpectedly: %v", err)
	}

	getDeleted := authedRequest(http.MethodGet, "/v2/products/product-1/firmwares/"+firmwareID, "", token)
	getDeletedResponse := httptest.NewRecorder()
	handler.ServeHTTP(getDeletedResponse, getDeleted)
	if getDeletedResponse.Code != http.StatusNotFound {
		t.Fatalf("get deleted status = %d body = %s", getDeletedResponse.Code, getDeletedResponse.Body.String())
	}
}

func TestProductFirmwareReleaseDefaultAndUpdateCheckRoutes(t *testing.T) {
	handler, token := newAuthenticatedFirmwareHandler(t)

	firstUpload := authedRequest(http.MethodPost, "/v2/products/product-1/firmwares?filename=v1.bin&version=1", "\x01", token)
	firstUpload.Header.Set("Content-Type", "application/octet-stream")
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, firstUpload)
	if firstResponse.Code != http.StatusCreated {
		t.Fatalf("first upload status = %d body = %s", firstResponse.Code, firstResponse.Body.String())
	}

	secondUpload := authedRequest(http.MethodPost, "/v2/products/product-1/firmwares?filename=v2.bin&version=2", "\x02", token)
	secondUpload.Header.Set("Content-Type", "application/octet-stream")
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, secondUpload)
	if secondResponse.Code != http.StatusCreated {
		t.Fatalf("second upload status = %d body = %s", secondResponse.Code, secondResponse.Body.String())
	}

	var second map[string]any
	if err := json.NewDecoder(secondResponse.Body).Decode(&second); err != nil {
		t.Fatalf("decode second upload: %v", err)
	}
	if second["released"] != false || second["default"] != false || second["current"] != false {
		t.Fatalf("second initial flags = %#v", second)
	}

	release := authedRequest(http.MethodPost, "/v1/products/product-1/firmware/"+second["id"].(string)+"/release", "", token)
	releaseResponse := httptest.NewRecorder()
	handler.ServeHTTP(releaseResponse, release)
	if releaseResponse.Code != http.StatusOK {
		t.Fatalf("release status = %d body = %s", releaseResponse.Code, releaseResponse.Body.String())
	}

	setDefault := authedRequest(http.MethodPut, "/v2/products/product-1/firmwares/"+second["id"].(string)+"/default", "", token)
	defaultResponse := httptest.NewRecorder()
	handler.ServeHTTP(defaultResponse, setDefault)
	if defaultResponse.Code != http.StatusOK {
		t.Fatalf("default status = %d body = %s", defaultResponse.Code, defaultResponse.Body.String())
	}
	var defaultFirmware map[string]any
	if err := json.NewDecoder(defaultResponse.Body).Decode(&defaultFirmware); err != nil {
		t.Fatalf("decode default firmware: %v", err)
	}
	if defaultFirmware["released"] != true || defaultFirmware["default"] != true || defaultFirmware["current"] != true {
		t.Fatalf("default flags = %#v", defaultFirmware)
	}

	check := authedRequest(http.MethodGet, "/v2/products/product-1/firmwares/check?version=1", "", token)
	checkResponse := httptest.NewRecorder()
	handler.ServeHTTP(checkResponse, check)
	if checkResponse.Code != http.StatusOK {
		t.Fatalf("check status = %d body = %s", checkResponse.Code, checkResponse.Body.String())
	}
	var update map[string]any
	if err := json.NewDecoder(checkResponse.Body).Decode(&update); err != nil {
		t.Fatalf("decode update check: %v", err)
	}
	if update["update_available"] != true || update["firmware_id"] != second["id"] || update["version"] != float64(2) {
		t.Fatalf("update check = %#v", update)
	}

	noUpdate := authedRequest(http.MethodGet, "/v1/products/product-1/firmware/check?version=2", "", token)
	noUpdateResponse := httptest.NewRecorder()
	handler.ServeHTTP(noUpdateResponse, noUpdate)
	if noUpdateResponse.Code != http.StatusOK {
		t.Fatalf("no-update status = %d body = %s", noUpdateResponse.Code, noUpdateResponse.Body.String())
	}
	var noUpdateBody map[string]any
	if err := json.NewDecoder(noUpdateResponse.Body).Decode(&noUpdateBody); err != nil {
		t.Fatalf("decode no-update check: %v", err)
	}
	if noUpdateBody["update_available"] != false || noUpdateBody["firmware_id"] != second["id"] {
		t.Fatalf("no-update check = %#v", noUpdateBody)
	}

	count := authedRequest(http.MethodGet, "/v2/products/product-1/firmwares/count", "", token)
	countResponse := httptest.NewRecorder()
	handler.ServeHTTP(countResponse, count)
	if countResponse.Code != http.StatusOK {
		t.Fatalf("count status = %d body = %s", countResponse.Code, countResponse.Body.String())
	}
	var firmwareCount float64
	if err := json.NewDecoder(countResponse.Body).Decode(&firmwareCount); err != nil {
		t.Fatalf("decode firmware count: %v", err)
	}
	if firmwareCount != 2 {
		t.Fatalf("firmware count = %v", firmwareCount)
	}

	getByVersion := authedRequest(http.MethodGet, "/v1/products/product-1/firmware/2", "", token)
	getByVersionResponse := httptest.NewRecorder()
	handler.ServeHTTP(getByVersionResponse, getByVersion)
	if getByVersionResponse.Code != http.StatusOK {
		t.Fatalf("get by version status = %d body = %s", getByVersionResponse.Code, getByVersionResponse.Body.String())
	}
	var versionFirmware map[string]any
	if err := json.NewDecoder(getByVersionResponse.Body).Decode(&versionFirmware); err != nil {
		t.Fatalf("decode version firmware: %v", err)
	}
	if versionFirmware["id"] != second["id"] || versionFirmware["version"] != float64(2) {
		t.Fatalf("version firmware = %#v", versionFirmware)
	}
}

func TestDeviceFlashJobRoutes(t *testing.T) {
	handler, token, deviceService := newAuthenticatedFirmwareAndDeviceHandler(t)

	upload := authedRequest(http.MethodPost, "/v2/products/product-1/firmwares?filename=flash.bin&current=true", "\x01\x02\x03", token)
	upload.Header.Set("Content-Type", "application/octet-stream")
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, upload)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload status = %d body = %s", uploadResponse.Code, uploadResponse.Body.String())
	}

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

	start := authedRequest(http.MethodPost, "/v1/devices/device-1/flash", `{"product_id":"product-1"}`, token)
	start.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	handler.ServeHTTP(startResponse, start)
	if startResponse.Code != http.StatusAccepted {
		t.Fatalf("start flash status = %d body = %s", startResponse.Code, startResponse.Body.String())
	}

	var job map[string]any
	if err := json.NewDecoder(startResponse.Body).Decode(&job); err != nil {
		t.Fatalf("decode flash job: %v", err)
	}
	if job["device_id"] != "device-1" || job["product_id"] != "product-1" || job["status"] != "queued" || job["progress"] != float64(0) || job["chunk_count"] != float64(1) {
		t.Fatalf("flash job = %#v", job)
	}

	list := authedRequest(http.MethodGet, "/v2/devices/device-1/flash", "", token)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list flash status = %d body = %s", listResponse.Code, listResponse.Body.String())
	}

	var jobs []map[string]any
	if err := json.NewDecoder(listResponse.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode flash jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0]["id"] != job["id"] {
		t.Fatalf("flash jobs = %#v", jobs)
	}

	get := authedRequest(http.MethodGet, "/v1/devices/device-1/flash/"+job["id"].(string), "", token)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get flash status = %d body = %s", getResponse.Code, getResponse.Body.String())
	}
}

func TestLegacyDeviceUpdateBinaryFlashRoute(t *testing.T) {
	handler, token, deviceService := newAuthenticatedFirmwareAndDeviceHandler(t)

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

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("product_id", "product-1"); err != nil {
		t.Fatalf("write product_id: %v", err)
	}
	file, err := writer.CreateFormFile("file", "custom.bin")
	if err != nil {
		t.Fatalf("create custom firmware file: %v", err)
	}
	if _, err := file.Write([]byte{0x01, 0x02, 0x03}); err != nil {
		t.Fatalf("write custom firmware: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request := authedRequest(http.MethodPut, "/v1/devices/device-1", "", token)
	request.Body = io.NopCloser(bytes.NewReader(body.Bytes()))
	request.ContentLength = int64(body.Len())
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("legacy flash status = %d body = %s", response.Code, response.Body.String())
	}

	var bodyResponse map[string]any
	if err := json.NewDecoder(response.Body).Decode(&bodyResponse); err != nil {
		t.Fatalf("decode legacy flash response: %v", err)
	}
	if bodyResponse["id"] != "device-1" || bodyResponse["status"] != "queued" || bodyResponse["flash_job_id"] == "" {
		t.Fatalf("legacy flash response = %#v", bodyResponse)
	}

	list := authedRequest(http.MethodGet, "/v1/devices/device-1/flash", "", token)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list flash status = %d body = %s", listResponse.Code, listResponse.Body.String())
	}
	var jobs []map[string]any
	if err := json.NewDecoder(listResponse.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode flash jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0]["product_id"] != "product-1" || jobs[0]["device_id"] != "device-1" {
		t.Fatalf("flash jobs = %#v", jobs)
	}
}

func newAuthenticatedFirmwareHandler(t *testing.T) (http.Handler, string) {
	t.Helper()

	handler, token, _ := newAuthenticatedFirmwareAndDeviceHandler(t)
	return handler, token
}

func newAuthenticatedFirmwareAndDeviceHandler(
	t *testing.T,
) (http.Handler, string, *devices.Service) {
	t.Helper()

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
	firmwareService := firmware.NewService(
		filerepo.NewProductFirmwareRepository(filepath.Join(dir, "firmware", "metadata")),
		filepath.Join(dir, "firmware", "binaries"),
		filerepo.NewFlashJobRepository(filepath.Join(dir, "firmware", "flashJobs")),
	)

	if err := authService.EnsureDefaultAdmin(context.Background(), "__admin__", "adminPassword"); err != nil {
		t.Fatalf("ensure default admin: %v", err)
	}

	handler := httpapi.NewHandlerWithFirmware(authService, deviceService, nil, firmwareService, slog.New(slog.NewTextHandler(io.Discard, nil)))
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

	return handler, body.AccessToken, deviceService
}
