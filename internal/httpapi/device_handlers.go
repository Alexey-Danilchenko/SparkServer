// Package httpapi contains device, claim, provisioning, and live command handlers.
package httpapi

import (
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"strings"

	"sparkserver/internal/auth"
	"sparkserver/internal/devices"
	"sparkserver/internal/events"
	"sparkserver/internal/firmware"
	"sparkserver/internal/products"
	"sparkserver/internal/webhooks"
)

func registerDeviceRoutes(
	router *http.ServeMux,
	authService *auth.Service,
	deviceService *devices.Service,
	firmwareService FirmwareService,
	keyRegistrar DeviceKeyRegistrar,
) {
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
}

func createDeviceClaimHandler(deviceService *devices.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claim, err := deviceService.CreateClaimCode(r.Context(), userFromContext(r.Context()).ID)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"claim_code": claim.Code,
			"expires_at": claim.ExpiresAt,
		})
	}
}

func provisionDeviceHandler(
	authService *auth.Service,
	deviceService *devices.Service,
	keyRegistrar DeviceKeyRegistrar,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, ok := provisioningRequestFromHTTP(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		if request.PublicKey != "" {
			// Particle clients can register a public key directly with authentication;
			// claim-code provisioning follows the older unauthenticated local-cloud path.
			if keyRegistrar == nil {
				writeError(w, http.StatusServiceUnavailable, "keys_unavailable")
				return
			}

			user, err := authenticateRequest(authService, r)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid_token")
				return
			}

			deviceID := r.PathValue("deviceID")
			if request.DeviceID != "" && request.DeviceID != deviceID {
				writeError(w, http.StatusBadRequest, "invalid_request")
				return
			}

			if err := keyRegistrar.SaveDevicePublicKeyPEM(deviceID, request.PublicKey); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_public_key")
				return
			}

			device, err := deviceService.Claim(r.Context(), user.ID, deviceID)
			if err != nil {
				writeServiceError(w, err)
				return
			}

			writeJSON(w, http.StatusOK, deviceResponse(device))
			return
		}

		if request.ClaimCode == "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		device, err := deviceService.Provision(
			r.Context(),
			r.PathValue("deviceID"),
			request.ClaimCode,
		)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, deviceResponse(device))
	}
}

type provisioningRequest struct {
	ClaimCode string
	DeviceID  string
	PublicKey string
}

type deviceUpdateRequest struct {
	Name       string
	Signal     string
	AppID      string
	ProductID  string
	File       multipart.File
	FileHeader *multipart.FileHeader
}

func claimDeviceHandler(deviceService *devices.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		deviceID, ok := deviceIDFromRequest(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		device, err := deviceService.Claim(r.Context(), user.ID, deviceID)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, deviceResponse(device))
	}
}

func listDevicesHandler(deviceService *devices.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		devices, err := deviceService.List(r.Context(), user.ID)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		response := make([]map[string]any, 0, len(devices))
		for index := range devices {
			response = append(response, deviceResponse(&devices[index]))
		}

		writeJSON(w, http.StatusOK, response)
	}
}

func getDeviceHandler(deviceService *devices.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, err := deviceService.Get(r.Context(), userFromContext(r.Context()).ID, r.PathValue("deviceIDorName"))
		if err != nil {
			writeServiceError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, deviceResponse(device))
	}
}

func updateDeviceHandler(
	deviceService *devices.Service,
	firmwareService FirmwareService,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, cleanup, ok := deviceUpdateFromRequest(r)
		if cleanup != nil {
			defer cleanup()
		}
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		user := userFromContext(r.Context())
		if request.Name != "" {
			device, err := deviceService.Update(r.Context(), user.ID, r.PathValue("deviceIDorName"), request.Name)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, deviceResponse(device))
			return
		}

		device, err := deviceService.Get(r.Context(), user.ID, r.PathValue("deviceIDorName"))
		if err != nil {
			writeServiceError(w, err)
			return
		}

		if request.Signal != "" {
			if request.Signal != "1" && request.Signal != "0" && request.Signal != "true" && request.Signal != "false" {
				writeError(w, http.StatusBadRequest, "invalid_signal")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"id": device.ID, "ok": true})
			return
		}

		if request.AppID != "" || request.File != nil {
			if firmwareService == nil {
				writeError(w, http.StatusServiceUnavailable, "firmware_unavailable")
				return
			}
			if !device.Connected {
				writeDeviceError(w, devices.ErrDeviceOffline)
				return
			}

			productID := firstString(request.ProductID, device.ProductID, device.ID)
			firmwareID := request.AppID
			if request.File != nil {
				filename := "firmware.bin"
				if request.FileHeader != nil && request.FileHeader.Filename != "" {
					filename = request.FileHeader.Filename
				}
				uploaded, err := firmwareService.UploadProductFirmware(r.Context(), firmware.Upload{
					ProductID: productID,
					Filename:  filename,
					Current:   true,
					Released:  true,
					Reader:    request.File,
				})
				if err != nil {
					writeServiceError(w, err)
					return
				}
				firmwareID = uploaded.ID
			}

			job, err := firmwareService.StartDeviceFlash(r.Context(), firmware.FlashRequest{
				DeviceID:   device.ID,
				ProductID:  productID,
				FirmwareID: firmwareID,
			})
			if err != nil {
				if errors.Is(err, devices.ErrDeviceOffline) || errors.Is(err, devices.ErrDeviceTimeout) {
					writeDeviceError(w, err)
					return
				}
				writeServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"id": device.ID, "status": job.Status, "flash_job_id": job.ID})
			return
		}

		writeError(w, http.StatusBadRequest, "invalid_request")
	}
}

func deleteDeviceHandler(deviceService *devices.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := deviceService.Unclaim(r.Context(), userFromContext(r.Context()).ID, r.PathValue("deviceIDorName")); err != nil {
			writeServiceError(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func pingDeviceHandler(deviceService *devices.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, err := deviceService.Ping(r.Context(), userFromContext(r.Context()).ID, r.PathValue("deviceIDorName"))
		if err != nil {
			if !errors.Is(err, devices.ErrDeviceOffline) {
				writeDeviceError(w, err)
				return
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"id":        device.ID,
			"connected": device.Connected,
			"online":    device.Connected,
		})
	}
}

func getDeviceVariableHandler(deviceService *devices.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceIDOrName := r.PathValue("deviceIDorName")
		variableName := r.PathValue("varName")

		value, err := deviceService.GetVariable(r.Context(), userFromContext(r.Context()).ID, deviceIDOrName, variableName)
		if err != nil {
			writeDeviceError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"cmd":    "VarReturn",
			"name":   variableName,
			"result": value,
		})
	}
}

func callDeviceFunctionHandler(deviceService *devices.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceIDOrName := r.PathValue("deviceIDorName")
		functionName := r.PathValue("functionName")

		argument, ok := functionArgumentFromRequest(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		returnValue, err := deviceService.CallFunction(r.Context(), userFromContext(r.Context()).ID, deviceIDOrName, functionName, argument)
		if err != nil {
			writeDeviceError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"id":           deviceIDOrName,
			"name":         functionName,
			"connected":    true,
			"return_value": returnValue,
		})
	}
}

func deviceIDFromRequest(r *http.Request) (string, bool) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			ID       string `json:"id"`
			DeviceID string `json:"deviceID"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return "", false
		}
		if body.ID != "" {
			return body.ID, true
		}
		return body.DeviceID, body.DeviceID != ""
	}

	if err := r.ParseForm(); err != nil {
		return "", false
	}

	deviceID := r.Form.Get("id")
	if deviceID == "" {
		deviceID = r.Form.Get("deviceID")
	}
	return deviceID, deviceID != ""
}

func functionArgumentFromRequest(r *http.Request) (string, bool) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Arg  string `json:"arg"`
			Args string `json:"args"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return "", false
		}
		if body.Arg != "" {
			return body.Arg, true
		}
		return body.Args, true
	}

	if err := r.ParseForm(); err != nil {
		return "", false
	}

	if r.Form.Has("arg") {
		return r.Form.Get("arg"), true
	}
	if r.Form.Has("args") {
		return r.Form.Get("args"), true
	}
	return "", true
}

func provisioningRequestFromHTTP(r *http.Request) (provisioningRequest, bool) {
	var request provisioningRequest
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			ClaimCode    string `json:"claim_code"`
			ClaimCodeAlt string `json:"claimCode"`
			DeviceID     string `json:"deviceID"`
			DeviceIDAlt  string `json:"device_id"`
			PublicKey    string `json:"publicKey"`
			PublicKeyAlt string `json:"public_key"`
			Key          string `json:"key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return request, false
		}
		request.ClaimCode = firstString(body.ClaimCode, body.ClaimCodeAlt)
		request.DeviceID = firstString(body.DeviceID, body.DeviceIDAlt)
		request.PublicKey = firstString(body.PublicKey, body.PublicKeyAlt, body.Key)
		return request, request.ClaimCode != "" || request.PublicKey != ""
	}

	if err := r.ParseForm(); err != nil {
		return request, false
	}

	request.ClaimCode = firstString(r.Form.Get("claim_code"), r.Form.Get("claimCode"))
	request.DeviceID = firstString(r.Form.Get("deviceID"), r.Form.Get("device_id"))
	request.PublicKey = firstString(r.Form.Get("publicKey"), r.Form.Get("public_key"), r.Form.Get("key"))
	return request, request.ClaimCode != "" || request.PublicKey != ""
}

func deviceUpdateFromRequest(r *http.Request) (deviceUpdateRequest, func(), bool) {
	var request deviceUpdateRequest
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var body struct {
			Name      string `json:"name"`
			Signal    string `json:"signal"`
			AppID     string `json:"app_id"`
			AppIDAlt  string `json:"appID"`
			ProductID string `json:"product_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return request, nil, false
		}
		request.Name = body.Name
		request.Signal = body.Signal
		request.AppID = firstString(body.AppID, body.AppIDAlt)
		request.ProductID = body.ProductID
		return request, nil, request.Name != "" || request.Signal != "" || request.AppID != ""
	}

	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			return request, nil, false
		}
		cleanup := func() {
			if request.File != nil {
				_ = request.File.Close()
			}
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}
		request.Name = r.Form.Get("name")
		request.Signal = r.Form.Get("signal")
		request.AppID = firstString(r.Form.Get("app_id"), r.Form.Get("appID"))
		request.ProductID = firstString(r.Form.Get("product_id"), r.Form.Get("productID"))
		file, header, ok := firstDeviceUpdateFile(r.MultipartForm)
		if ok {
			request.File = file
			request.FileHeader = header
		}
		return request, cleanup, request.Name != "" || request.Signal != "" || request.AppID != "" || request.File != nil
	}

	if err := r.ParseForm(); err != nil {
		return request, nil, false
	}
	request.Name = r.Form.Get("name")
	request.Signal = r.Form.Get("signal")
	request.AppID = firstString(r.Form.Get("app_id"), r.Form.Get("appID"))
	request.ProductID = firstString(r.Form.Get("product_id"), r.Form.Get("productID"))
	return request, nil, request.Name != "" || request.Signal != "" || request.AppID != ""
}

func firstDeviceUpdateFile(form *multipart.Form) (multipart.File, *multipart.FileHeader, bool) {
	if form == nil {
		return nil, nil, false
	}
	for _, key := range []string{"file", "binary", "firmware"} {
		files := form.File[key]
		if len(files) == 0 {
			continue
		}
		file, err := files[0].Open()
		if err != nil {
			return nil, nil, false
		}
		return file, files[0], true
	}
	for _, files := range form.File {
		if len(files) == 0 {
			continue
		}
		file, err := files[0].Open()
		if err != nil {
			return nil, nil, false
		}
		return file, files[0], true
	}
	return nil, nil, false
}

func deviceNameFromRequest(r *http.Request) (string, bool) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return "", false
		}
		return body.Name, body.Name != ""
	}

	if err := r.ParseForm(); err != nil {
		return "", false
	}

	name := r.Form.Get("name")
	return name, name != ""
}

func deviceResponse(device *devices.Device) map[string]any {
	response := map[string]any{
		"id":         device.ID,
		"name":       device.Name,
		"connected":  device.Connected,
		"online":     device.Connected,
		"product_id": device.ProductID,
		"variables":  device.Variables,
		"functions":  device.Functions,
	}

	if device.LastHeardAt != nil {
		response["last_heard"] = device.LastHeardAt
	}

	return response
}

func writeServiceError(w http.ResponseWriter, err error) {
	if errors.Is(err, auth.ErrNotFound) ||
		errors.Is(err, devices.ErrNotFound) ||
		errors.Is(err, events.ErrNotFound) ||
		errors.Is(err, firmware.ErrNotFound) ||
		errors.Is(err, products.ErrNotFound) ||
		errors.Is(err, webhooks.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if errors.Is(err, auth.ErrConflict) ||
		errors.Is(err, devices.ErrConflict) ||
		errors.Is(err, events.ErrConflict) ||
		errors.Is(err, firmware.ErrConflict) ||
		errors.Is(err, products.ErrConflict) ||
		errors.Is(err, webhooks.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict")
		return
	}
	writeError(w, http.StatusInternalServerError, "server_error")
}

func writeDeviceError(w http.ResponseWriter, err error) {
	if errors.Is(err, devices.ErrDeviceTimeout) {
		writeError(w, http.StatusRequestTimeout, "device_timeout")
		return
	}
	if errors.Is(err, devices.ErrDeviceOffline) {
		writeError(w, http.StatusRequestTimeout, "device_offline")
		return
	}
	writeServiceError(w, err)
}
