// Package httpapi contains product/fleet route handlers.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"sparkserver/internal/domain"
	"sparkserver/internal/products"
)

// ProductService is the HTTP-facing subset implemented by products.Service.
type ProductService interface {
	Create(ctx context.Context, request products.CreateRequest) (*domain.Product, error)
	List(ctx context.Context, ownerID string) ([]domain.Product, error)
	Get(ctx context.Context, ownerID string, idOrSlug string) (*domain.Product, error)
	Config(ctx context.Context, ownerID string, idOrSlug string) (map[string]any, error)
	Update(ctx context.Context, ownerID string, idOrSlug string, request products.UpdateRequest) (*domain.Product, error)
	Delete(ctx context.Context, ownerID string, idOrSlug string) error
	AddDevice(ctx context.Context, ownerID string, productIDOrSlug string, deviceID string) (*domain.ProductDevice, error)
	ListDevices(ctx context.Context, ownerID string, productIDOrSlug string) ([]domain.Device, error)
	GetDevice(ctx context.Context, ownerID string, productIDOrSlug string, deviceID string) (*domain.Device, *domain.ProductDevice, error)
	UpdateDevice(ctx context.Context, ownerID string, productIDOrSlug string, deviceID string, request products.ProductDeviceUpdateRequest) (*domain.ProductDevice, error)
	RemoveDevice(ctx context.Context, ownerID string, productIDOrSlug string, deviceID string) error
}

func listProductsHandler(productService ProductService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if productService == nil {
			writeError(w, http.StatusServiceUnavailable, "products_unavailable")
			return
		}

		products, err := productService.List(r.Context(), userFromContext(r.Context()).ID)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}

		response := make([]map[string]any, 0, len(products))
		for index := range products {
			response = append(response, productResponse(&products[index]))
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func createProductHandler(productService ProductService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if productService == nil {
			writeError(w, http.StatusServiceUnavailable, "products_unavailable")
			return
		}

		request, ok := productCreateRequestFromHTTP(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		request.OwnerID = userFromContext(r.Context()).ID

		product, err := productService.Create(r.Context(), request)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, productResponse(product))
	}
}

func getProductHandler(productService ProductService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if productService == nil {
			writeError(w, http.StatusServiceUnavailable, "products_unavailable")
			return
		}

		product, err := productService.Get(r.Context(), userFromContext(r.Context()).ID, r.PathValue("productIDOrSlug"))
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, productResponse(product))
	}
}

func getProductConfigHandler(productService ProductService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if productService == nil {
			writeError(w, http.StatusServiceUnavailable, "products_unavailable")
			return
		}

		config, err := productService.Config(r.Context(), userFromContext(r.Context()).ID, r.PathValue("productIDOrSlug"))
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"product_configuration": config})
	}
}

func updateProductHandler(productService ProductService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if productService == nil {
			writeError(w, http.StatusServiceUnavailable, "products_unavailable")
			return
		}

		request, ok := productUpdateRequestFromHTTP(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		product, err := productService.Update(r.Context(), userFromContext(r.Context()).ID, r.PathValue("productIDOrSlug"), request)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, productResponse(product))
	}
}

func deleteProductHandler(productService ProductService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if productService == nil {
			writeError(w, http.StatusServiceUnavailable, "products_unavailable")
			return
		}

		if err := productService.Delete(r.Context(), userFromContext(r.Context()).ID, r.PathValue("productIDOrSlug")); err != nil {
			writeRepositoryError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func listProductDevicesHandler(productService ProductService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if productService == nil {
			writeError(w, http.StatusServiceUnavailable, "products_unavailable")
			return
		}

		devices, err := productService.ListDevices(r.Context(), userFromContext(r.Context()).ID, r.PathValue("productIDOrSlug"))
		if err != nil {
			writeRepositoryError(w, err)
			return
		}

		response := make([]map[string]any, 0, len(devices))
		for index := range devices {
			response = append(response, deviceResponse(&devices[index]))
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func getProductDeviceHandler(productService ProductService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if productService == nil {
			writeError(w, http.StatusServiceUnavailable, "products_unavailable")
			return
		}

		device, link, err := productService.GetDevice(r.Context(), userFromContext(r.Context()).ID, r.PathValue("productIDOrSlug"), r.PathValue("deviceID"))
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, productDeviceDetailResponse(device, link))
	}
}

func addProductDeviceHandler(productService ProductService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if productService == nil {
			writeError(w, http.StatusServiceUnavailable, "products_unavailable")
			return
		}

		deviceID, ok := productDeviceIDFromHTTP(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		link, err := productService.AddDevice(r.Context(), userFromContext(r.Context()).ID, r.PathValue("productIDOrSlug"), deviceID)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, productDeviceResponse(link))
	}
}

func updateProductDeviceHandler(
	productService  ProductService,
	firmwareService FirmwareService,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if productService == nil {
			writeError(w, http.StatusServiceUnavailable, "products_unavailable")
			return
		}

		request, ok := productDeviceUpdateFromHTTP(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		link, err := productService.UpdateDevice(r.Context(), userFromContext(r.Context()).ID, r.PathValue("productIDOrSlug"), r.PathValue("deviceID"), request)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		if firmwareService != nil && shouldTriggerProductFirmwareUpdate(request) {
			device, _, err := productService.GetDevice(r.Context(), userFromContext(r.Context()).ID, r.PathValue("productIDOrSlug"), r.PathValue("deviceID"))
			if err == nil {
				_, _, _ = firmwareService.CheckAndStartProductFirmwareUpdate(r.Context(), device)
			}
		}
		writeJSON(w, http.StatusOK, productDeviceResponse(link))
	}
}

func shouldTriggerProductFirmwareUpdate(request products.ProductDeviceUpdateRequest) bool {
	return request.DesiredFirmwareVersion != nil || request.ClearDesiredFirmware || request.Quarantined != nil
}

func removeProductDeviceHandler(productService ProductService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if productService == nil {
			writeError(w, http.StatusServiceUnavailable, "products_unavailable")
			return
		}

		if err := productService.RemoveDevice(r.Context(), userFromContext(r.Context()).ID, r.PathValue("productIDOrSlug"), r.PathValue("deviceID")); err != nil {
			writeRepositoryError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func unsupportedProductFeatureHandler(message string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotImplemented, message)
	}
}

func productCreateRequestFromHTTP(r *http.Request) (products.CreateRequest, bool) {
	var request products.CreateRequest
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			ID          string `json:"id"`
			ProductID   string `json:"product_id"`
			Slug        string `json:"slug"`
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return request, false
		}
		request.ID = firstString(body.ID, body.ProductID)
		request.Slug = body.Slug
		request.Name = body.Name
		request.Description = body.Description
		return request, request.Slug != "" || request.Name != ""
	}

	if err := r.ParseForm(); err != nil {
		return request, false
	}
	request.ID = firstString(r.Form.Get("id"), r.Form.Get("product_id"))
	request.Slug = r.Form.Get("slug")
	request.Name = r.Form.Get("name")
	request.Description = r.Form.Get("description")
	return request, request.Slug != "" || request.Name != ""
}

func productUpdateRequestFromHTTP(r *http.Request) (products.UpdateRequest, bool) {
	var request products.UpdateRequest
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			return request, false
		}
		return request, request.Name != "" || request.Slug != "" || request.Description != ""
	}

	if err := r.ParseForm(); err != nil {
		return request, false
	}
	request.Name = r.Form.Get("name")
	request.Slug = r.Form.Get("slug")
	request.Description = r.Form.Get("description")
	return request, request.Name != "" || request.Slug != "" || request.Description != ""
}

func productDeviceIDFromHTTP(r *http.Request) (string, bool) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			ID       string `json:"id"`
			DeviceID string `json:"device_id"`
			CoreID   string `json:"coreid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return "", false
		}
		deviceID := firstString(body.DeviceID, body.ID, body.CoreID)
		return deviceID, deviceID != ""
	}

	if err := r.ParseForm(); err != nil {
		return "", false
	}
	deviceID := firstString(r.Form.Get("device_id"), r.Form.Get("deviceID"), r.Form.Get("id"), r.Form.Get("coreid"))
	return deviceID, deviceID != ""
}

func productDeviceUpdateFromHTTP(r *http.Request) (products.ProductDeviceUpdateRequest, bool) {
	var request products.ProductDeviceUpdateRequest
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Notes                  *string         `json:"notes"`
			Denied                 *bool           `json:"denied"`
			Development            *bool           `json:"development"`
			Quarantined            *bool           `json:"quarantined"`
			DesiredFirmwareVersion json.RawMessage `json:"desired_firmware_version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return request, false
		}
		request.Notes = body.Notes
		request.Denied = body.Denied
		request.Development = body.Development
		request.Quarantined = body.Quarantined
		if body.DesiredFirmwareVersion != nil {
			if string(body.DesiredFirmwareVersion) == "null" {
				request.ClearDesiredFirmware = true
			} else if err := json.Unmarshal(body.DesiredFirmwareVersion, &request.DesiredFirmwareVersion); err != nil {
				return request, false
			}
		}
		return request, body.Notes != nil || body.Denied != nil || body.Development != nil || body.Quarantined != nil || body.DesiredFirmwareVersion != nil || request.ClearDesiredFirmware
	}

	if err := r.ParseForm(); err != nil {
		return request, false
	}
	if r.Form.Has("notes") {
		notes := r.Form.Get("notes")
		request.Notes = &notes
	}
	if r.Form.Has("denied") {
		value := boolFromString(r.Form.Get("denied"))
		request.Denied = &value
	}
	if r.Form.Has("development") {
		value := boolFromString(r.Form.Get("development"))
		request.Development = &value
	}
	if r.Form.Has("quarantined") {
		value := boolFromString(r.Form.Get("quarantined"))
		request.Quarantined = &value
	}
	if r.Form.Has("desired_firmware_version") {
		version, ok := intFromString(r.Form.Get("desired_firmware_version"))
		if !ok {
			return request, false
		}
		request.DesiredFirmwareVersion = &version
	}
	return request, request.Notes != nil || request.Denied != nil || request.Development != nil || request.Quarantined != nil || request.DesiredFirmwareVersion != nil
}

func productResponse(product *domain.Product) map[string]any {
	return map[string]any{
		"id":          product.ID,
		"slug":        product.Slug,
		"name":        product.Name,
		"description": product.Description,
		"owner_id":    product.OwnerID,
		"created_at":  product.CreatedAt,
		"updated_at":  product.UpdatedAt,
	}
}

func productDeviceResponse(device *domain.ProductDevice) map[string]any {
	response := map[string]any{
		"id":          device.ID,
		"product_id":  device.ProductID,
		"device_id":   device.DeviceID,
		"owner_id":    device.OwnerID,
		"notes":       device.Notes,
		"denied":      device.Denied,
		"development": device.Development,
		"quarantined": device.Quarantined,
		"created_at":  device.CreatedAt,
		"updated_at":  device.UpdatedAt,
	}
	if device.DesiredFirmwareVersion != nil {
		response["desired_firmware_version"] = *device.DesiredFirmwareVersion
	}
	return response
}

func productDeviceDetailResponse(device *domain.Device, link *domain.ProductDevice) map[string]any {
	response := deviceResponse(device)
	for key, value := range productDeviceResponse(link) {
		response[key] = value
	}
	response["id"] = device.ID
	response["product_id"] = link.ProductID
	return response
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
