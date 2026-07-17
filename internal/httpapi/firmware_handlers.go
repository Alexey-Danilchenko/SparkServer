// Package httpapi contains product firmware metadata and OTA flash-job handlers.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"sparkserver/internal/domain"
	"sparkserver/internal/firmware"
)

// FirmwareService is the HTTP-facing subset implemented by firmware.Service.
type FirmwareService interface {
	UploadProductFirmware(ctx context.Context, upload firmware.Upload) (*domain.ProductFirmware, error)
	ListProductFirmware(ctx context.Context, productID string) ([]domain.ProductFirmware, error)
	GetProductFirmware(ctx context.Context, productID string, firmwareID string) (*domain.ProductFirmware, error)
	UpdateProductFirmware(ctx context.Context, productID string, firmwareID string, update firmware.Update) (*domain.ProductFirmware, error)
	DeleteProductFirmware(ctx context.Context, productID string, firmwareID string) error
	ReleaseProductFirmware(ctx context.Context, productID string, firmwareID string) (*domain.ProductFirmware, error)
	SetDefaultProductFirmware(ctx context.Context, productID string, firmwareID string) (*domain.ProductFirmware, error)
	CheckProductFirmwareUpdate(ctx context.Context, request firmware.UpdateCheckRequest) (*domain.ProductFirmware, bool, error)
	CheckAndStartProductFirmwareUpdate(ctx context.Context, device *domain.Device) (*domain.FlashJob, bool, error)
	StartDeviceFlash(ctx context.Context, request firmware.FlashRequest) (*domain.FlashJob, error)
	ListDeviceFlashJobs(ctx context.Context, deviceID string) ([]domain.FlashJob, error)
	GetDeviceFlashJob(ctx context.Context, deviceID string, jobID string) (*domain.FlashJob, error)
}

func listProductFirmwaresHandler(firmwareService FirmwareService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if firmwareService == nil {
			writeError(w, http.StatusServiceUnavailable, "firmware_unavailable")
			return
		}

		firmwares, err := firmwareService.ListProductFirmware(r.Context(), r.PathValue("productIDOrSlug"))
		if err != nil {
			writeRepositoryError(w, err)
			return
		}

		response := make([]map[string]any, 0, len(firmwares))
		for index := range firmwares {
			response = append(response, firmwareResponse(&firmwares[index]))
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func countProductFirmwaresHandler(firmwareService FirmwareService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if firmwareService == nil {
			writeError(w, http.StatusServiceUnavailable, "firmware_unavailable")
			return
		}

		firmwares, err := firmwareService.ListProductFirmware(r.Context(), r.PathValue("productIDOrSlug"))
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, len(firmwares))
	}
}

func uploadProductFirmwareHandler(firmwareService FirmwareService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if firmwareService == nil {
			writeError(w, http.StatusServiceUnavailable, "firmware_unavailable")
			return
		}

		upload, cleanup, ok := firmwareUploadFromRequest(r)
		if cleanup != nil {
			defer cleanup()
		}
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		upload.ProductID = r.PathValue("productIDOrSlug")
		firmware, err := firmwareService.UploadProductFirmware(r.Context(), upload)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, firmwareResponse(firmware))
	}
}

func getProductFirmwareHandler(firmwareService FirmwareService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if firmwareService == nil {
			writeError(w, http.StatusServiceUnavailable, "firmware_unavailable")
			return
		}

		firmware, err := firmwareService.GetProductFirmware(
			r.Context(),
			r.PathValue("productIDOrSlug"),
			r.PathValue("firmwareID"),
		)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, firmwareResponse(firmware))
	}
}

func updateProductFirmwareHandler(firmwareService FirmwareService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if firmwareService == nil {
			writeError(w, http.StatusServiceUnavailable, "firmware_unavailable")
			return
		}

		update, cleanup, ok := firmwareUpdateFromRequest(r)
		if cleanup != nil {
			defer cleanup()
		}
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		firmware, err := firmwareService.UpdateProductFirmware(
			r.Context(),
			r.PathValue("productIDOrSlug"),
			r.PathValue("firmwareID"),
			update,
		)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, firmwareResponse(firmware))
	}
}

func deleteProductFirmwareHandler(firmwareService FirmwareService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if firmwareService == nil {
			writeError(w, http.StatusServiceUnavailable, "firmware_unavailable")
			return
		}

		if err := firmwareService.DeleteProductFirmware(
			r.Context(),
			r.PathValue("productIDOrSlug"),
			r.PathValue("firmwareID"),
		); err != nil {
			writeRepositoryError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func releaseProductFirmwareHandler(firmwareService FirmwareService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if firmwareService == nil {
			writeError(w, http.StatusServiceUnavailable, "firmware_unavailable")
			return
		}

		firmware, err := firmwareService.ReleaseProductFirmware(
			r.Context(),
			r.PathValue("productIDOrSlug"),
			r.PathValue("firmwareID"),
		)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, firmwareResponse(firmware))
	}
}

func defaultProductFirmwareHandler(firmwareService FirmwareService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if firmwareService == nil {
			writeError(w, http.StatusServiceUnavailable, "firmware_unavailable")
			return
		}

		firmware, err := firmwareService.SetDefaultProductFirmware(
			r.Context(),
			r.PathValue("productIDOrSlug"),
			r.PathValue("firmwareID"),
		)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, firmwareResponse(firmware))
	}
}

func checkProductFirmwareUpdateHandler(firmwareService FirmwareService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if firmwareService == nil {
			writeError(w, http.StatusServiceUnavailable, "firmware_unavailable")
			return
		}

		request, ok := updateCheckRequestFromHTTP(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		request.ProductID = r.PathValue("productIDOrSlug")

		target, updateAvailable, err := firmwareService.CheckProductFirmwareUpdate(r.Context(), request)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}

		response := map[string]any{"update_available": updateAvailable}
		if target != nil {
			response["firmware"] = firmwareResponse(target)
			response["firmware_id"] = target.ID
			response["version"] = target.Version
			response["url"] = "/v1/products/" + target.ProductID + "/firmware/" + target.ID
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func startDeviceFlashHandler(
	deviceService   DeviceResolver,
	firmwareService FirmwareService,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if firmwareService == nil {
			writeError(w, http.StatusServiceUnavailable, "firmware_unavailable")
			return
		}
		if deviceService == nil {
			writeError(w, http.StatusServiceUnavailable, "devices_unavailable")
			return
		}

		user := userFromContext(r.Context())
		device, err := deviceService.Get(r.Context(), user.ID, r.PathValue("deviceIDorName"))
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		if !device.Connected {
			writeDeviceError(w, domain.ErrDeviceOffline)
			return
		}

		request, ok := flashRequestFromHTTP(r, device)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		job, err := firmwareService.StartDeviceFlash(r.Context(), request)
		if err != nil {
			if errors.Is(err, domain.ErrDeviceOffline) || errors.Is(err, domain.ErrDeviceTimeout) {
				writeDeviceError(w, err)
				return
			}
			writeRepositoryError(w, err)
			return
		}

		writeJSON(w, http.StatusAccepted, flashJobResponse(job))
	}
}

func listDeviceFlashJobsHandler(
	deviceService   DeviceResolver,
	firmwareService FirmwareService,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if firmwareService == nil {
			writeError(w, http.StatusServiceUnavailable, "firmware_unavailable")
			return
		}
		if deviceService == nil {
			writeError(w, http.StatusServiceUnavailable, "devices_unavailable")
			return
		}

		user := userFromContext(r.Context())
		device, err := deviceService.Get(r.Context(), user.ID, r.PathValue("deviceIDorName"))
		if err != nil {
			writeRepositoryError(w, err)
			return
		}

		jobs, err := firmwareService.ListDeviceFlashJobs(r.Context(), device.ID)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}

		response := make([]map[string]any, 0, len(jobs))
		for index := range jobs {
			response = append(response, flashJobResponse(&jobs[index]))
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func getDeviceFlashJobHandler(
	deviceService   DeviceResolver,
	firmwareService FirmwareService,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if firmwareService == nil {
			writeError(w, http.StatusServiceUnavailable, "firmware_unavailable")
			return
		}
		if deviceService == nil {
			writeError(w, http.StatusServiceUnavailable, "devices_unavailable")
			return
		}

		user := userFromContext(r.Context())
		device, err := deviceService.Get(r.Context(), user.ID, r.PathValue("deviceIDorName"))
		if err != nil {
			writeRepositoryError(w, err)
			return
		}

		job, err := firmwareService.GetDeviceFlashJob(r.Context(), device.ID, r.PathValue("jobID"))
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, flashJobResponse(job))
	}
}

type DeviceResolver interface {
	Get(ctx context.Context, ownerID string, idOrName string) (*domain.Device, error)
}

func flashRequestFromHTTP(r *http.Request, device *domain.Device) (firmware.FlashRequest, bool) {
	request := firmware.FlashRequest{
		DeviceID:  device.ID,
		ProductID: device.ProductID,
	}

	if r.URL.Query().Get("product_id") != "" {
		request.ProductID = r.URL.Query().Get("product_id")
	}
	if r.URL.Query().Get("productID") != "" {
		request.ProductID = r.URL.Query().Get("productID")
	}
	if r.URL.Query().Get("firmware_id") != "" {
		request.FirmwareID = r.URL.Query().Get("firmware_id")
	}
	if r.URL.Query().Get("firmwareID") != "" {
		request.FirmwareID = r.URL.Query().Get("firmwareID")
	}

	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			ProductID     string `json:"product_id"`
			ProductIDAlt  string `json:"productID"`
			FirmwareID    string `json:"firmware_id"`
			FirmwareIDAlt string `json:"firmwareID"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return firmware.FlashRequest{}, false
		}
		if body.ProductID != "" {
			request.ProductID = body.ProductID
		}
		if body.ProductIDAlt != "" {
			request.ProductID = body.ProductIDAlt
		}
		if body.FirmwareID != "" {
			request.FirmwareID = body.FirmwareID
		}
		if body.FirmwareIDAlt != "" {
			request.FirmwareID = body.FirmwareIDAlt
		}
	}

	return request, request.DeviceID != "" && request.ProductID != ""
}

func firmwareUploadFromRequest(r *http.Request) (firmware.Upload, func(), bool) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return multipartFirmwareUploadFromRequest(r)
	}

	version, ok := intFromString(r.URL.Query().Get("version"))
	if !ok && r.URL.Query().Get("version") != "" {
		return firmware.Upload{}, nil, false
	}

	filename := r.URL.Query().Get("filename")
	if filename == "" {
		filename = r.URL.Query().Get("file")
	}
	if filename == "" {
		filename = "firmware.bin"
	}

	return firmware.Upload{
		Version:      version,
		Title:        r.URL.Query().Get("title"),
		Description:  r.URL.Query().Get("description"),
		Filename:     filename,
		ContentType:  r.Header.Get("Content-Type"),
		ReleaseNotes: r.URL.Query().Get("release_notes"),
		Current:      boolFromString(r.URL.Query().Get("current")),
		Released:     boolFromString(firstNonEmptyString(r.URL.Query().Get("released"), r.URL.Query().Get("release"))),
		Default:      boolFromString(firstNonEmptyString(r.URL.Query().Get("default"), r.URL.Query().Get("default_firmware"))),
		Reader:       r.Body,
	}, nil, true
}

func multipartFirmwareUploadFromRequest(r *http.Request) (firmware.Upload, func(), bool) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		return firmware.Upload{}, nil, false
	}

	file, header, ok := firstFirmwareFile(r.MultipartForm)
	if !ok {
		return firmware.Upload{}, nil, false
	}

	cleanup := func() {
		_ = file.Close()
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}

	version, ok := intFromString(firstFormValue(r, "version"))
	if !ok && firstFormValue(r, "version") != "" {
		return firmware.Upload{}, cleanup, false
	}

	return firmware.Upload{
		Version:      version,
		Title:        firstFormValue(r, "title"),
		Description:  firstFormValue(r, "description"),
		Filename:     header.Filename,
		ContentType:  header.Header.Get("Content-Type"),
		ReleaseNotes: firstNonEmptyString(firstFormValue(r, "release_notes"), firstFormValue(r, "releaseNotes")),
		Current:      boolFromString(firstFormValue(r, "current")),
		Released:     boolFromString(firstNonEmptyString(firstFormValue(r, "released"), firstFormValue(r, "release"))),
		Default:      boolFromString(firstNonEmptyString(firstFormValue(r, "default"), firstFormValue(r, "default_firmware"))),
		Reader:       file,
	}, cleanup, true
}

func firmwareUpdateFromRequest(r *http.Request) (firmware.Update, func(), bool) {
	update := firmwareUpdateFromQuery(r)
	hasUpdate := firmwareUpdateHasChanges(update)
	contentType := r.Header.Get("Content-Type")

	if strings.HasPrefix(contentType, "application/json") {
		var body struct {
			Version         *int    `json:"version"`
			Title           *string `json:"title"`
			Description     *string `json:"description"`
			Filename        *string `json:"filename"`
			ContentType     *string `json:"content_type"`
			ReleaseNotes    *string `json:"release_notes"`
			ReleaseNotesAlt *string `json:"releaseNotes"`
			Released        *bool   `json:"released"`
			Release         *bool   `json:"release"`
			Default         *bool   `json:"default"`
			Current         *bool   `json:"current"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return firmware.Update{}, nil, false
		}
		applyFirmwareUpdateJSON(&update, body)
		return update, nil, firmwareUpdateHasChanges(update)
	}

	if strings.HasPrefix(contentType, "multipart/form-data") {
		return multipartFirmwareUpdateFromRequest(r, update, hasUpdate)
	}

	if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
		if err := r.ParseForm(); err != nil {
			return firmware.Update{}, nil, false
		}
		applyFirmwareUpdateForm(&update, r)
		return update, nil, firmwareUpdateHasChanges(update)
	}

	if r.Body != nil && contentType != "" {
		filename := firstNonEmptyString(r.URL.Query().Get("filename"), r.URL.Query().Get("file"), "firmware.bin")
		update.Filename = &filename
		update.ContentType = &contentType
		update.Reader = r.Body
		return update, nil, true
	}

	return update, nil, hasUpdate
}

func multipartFirmwareUpdateFromRequest(
	r         *http.Request,
	update    firmware.Update,
	hasUpdate bool,
) (firmware.Update, func(), bool) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		return firmware.Update{}, nil, false
	}

	cleanup := func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}

	applyFirmwareUpdateForm(&update, r)
	hasUpdate = hasUpdate || firmwareUpdateHasChanges(update)

	file, header, ok := firstFirmwareFile(r.MultipartForm)
	if ok {
		previousCleanup := cleanup
		cleanup = func() {
			_ = file.Close()
			previousCleanup()
		}
		filename := header.Filename
		contentType := header.Header.Get("Content-Type")
		update.Filename = &filename
		update.ContentType = &contentType
		update.Reader = file
		hasUpdate = true
	}

	return update, cleanup, hasUpdate
}

func firmwareUpdateFromQuery(r *http.Request) firmware.Update {
	update := firmware.Update{}
	if r.URL.Query().Has("version") {
		if version, ok := intFromString(r.URL.Query().Get("version")); ok {
			update.Version = &version
		}
	}
	if r.URL.Query().Has("title") {
		value := r.URL.Query().Get("title")
		update.Title = &value
	}
	if r.URL.Query().Has("description") {
		value := r.URL.Query().Get("description")
		update.Description = &value
	}
	if r.URL.Query().Has("filename") || r.URL.Query().Has("file") {
		value := firstNonEmptyString(r.URL.Query().Get("filename"), r.URL.Query().Get("file"))
		update.Filename = &value
	}
	if r.URL.Query().Has("release_notes") || r.URL.Query().Has("releaseNotes") {
		value := firstNonEmptyString(r.URL.Query().Get("release_notes"), r.URL.Query().Get("releaseNotes"))
		update.ReleaseNotes = &value
	}
	if r.URL.Query().Has("released") || r.URL.Query().Has("release") {
		value := boolFromString(firstNonEmptyString(r.URL.Query().Get("released"), r.URL.Query().Get("release")))
		update.Released = &value
	}
	if r.URL.Query().Has("default") || r.URL.Query().Has("default_firmware") {
		value := boolFromString(firstNonEmptyString(r.URL.Query().Get("default"), r.URL.Query().Get("default_firmware")))
		update.Default = &value
	}
	if r.URL.Query().Has("current") {
		value := boolFromString(r.URL.Query().Get("current"))
		update.Current = &value
	}
	return update
}

func applyFirmwareUpdateForm(update *firmware.Update, r *http.Request) {
	if r.Form.Has("version") {
		if version, ok := intFromString(r.Form.Get("version")); ok {
			update.Version = &version
		}
	}
	if r.Form.Has("title") {
		value := r.Form.Get("title")
		update.Title = &value
	}
	if r.Form.Has("description") {
		value := r.Form.Get("description")
		update.Description = &value
	}
	if r.Form.Has("filename") || r.Form.Has("file") {
		value := firstNonEmptyString(r.Form.Get("filename"), r.Form.Get("file"))
		update.Filename = &value
	}
	if r.Form.Has("release_notes") || r.Form.Has("releaseNotes") {
		value := firstNonEmptyString(r.Form.Get("release_notes"), r.Form.Get("releaseNotes"))
		update.ReleaseNotes = &value
	}
	if r.Form.Has("released") || r.Form.Has("release") {
		value := boolFromString(firstNonEmptyString(r.Form.Get("released"), r.Form.Get("release")))
		update.Released = &value
	}
	if r.Form.Has("default") || r.Form.Has("default_firmware") {
		value := boolFromString(firstNonEmptyString(r.Form.Get("default"), r.Form.Get("default_firmware")))
		update.Default = &value
	}
	if r.Form.Has("current") {
		value := boolFromString(r.Form.Get("current"))
		update.Current = &value
	}
}

func applyFirmwareUpdateJSON(update *firmware.Update, body struct {
	Version         *int    `json:"version"`
	Title           *string `json:"title"`
	Description     *string `json:"description"`
	Filename        *string `json:"filename"`
	ContentType     *string `json:"content_type"`
	ReleaseNotes    *string `json:"release_notes"`
	ReleaseNotesAlt *string `json:"releaseNotes"`
	Released        *bool   `json:"released"`
	Release         *bool   `json:"release"`
	Default         *bool   `json:"default"`
	Current         *bool   `json:"current"`
}) {
	update.Version = firstIntPointer(body.Version, update.Version)
	update.Title = firstStringPointer(body.Title, update.Title)
	update.Description = firstStringPointer(body.Description, update.Description)
	update.Filename = firstStringPointer(body.Filename, update.Filename)
	update.ContentType = firstStringPointer(body.ContentType, update.ContentType)
	update.ReleaseNotes = firstStringPointer(body.ReleaseNotes, body.ReleaseNotesAlt, update.ReleaseNotes)
	update.Released = firstBoolPointer(body.Released, body.Release, update.Released)
	update.Default = firstBoolPointer(body.Default, update.Default)
	update.Current = firstBoolPointer(body.Current, update.Current)
}

func firmwareUpdateHasChanges(update firmware.Update) bool {
	return update.Version != nil ||
		update.Title != nil ||
		update.Description != nil ||
		update.Filename != nil ||
		update.ContentType != nil ||
		update.ReleaseNotes != nil ||
		update.Released != nil ||
		update.Default != nil ||
		update.Current != nil ||
		update.Reader != nil
}

func firstStringPointer(values ...*string) *string {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstBoolPointer(values ...*bool) *bool {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstIntPointer(values ...*int) *int {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstFirmwareFile(form *multipart.Form) (multipart.File, *multipart.FileHeader, bool) {
	if form == nil {
		return nil, nil, false
	}

	for _, name := range []string{"file", "binary", "firmware", "firmware.bin"} {
		headers := form.File[name]
		if len(headers) == 0 {
			continue
		}
		file, err := headers[0].Open()
		if err != nil {
			return nil, nil, false
		}
		return file, headers[0], true
	}

	for _, headers := range form.File {
		if len(headers) == 0 {
			continue
		}
		file, err := headers[0].Open()
		if err != nil {
			return nil, nil, false
		}
		return file, headers[0], true
	}

	return nil, nil, false
}

func firstFormValue(r *http.Request, name string) string {
	if r.MultipartForm == nil || r.MultipartForm.Value == nil {
		return ""
	}
	values := r.MultipartForm.Value[name]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func intFromString(value string) (int, bool) {
	if value == "" {
		return 0, true
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func boolFromString(value string) bool {
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func updateCheckRequestFromHTTP(r *http.Request) (firmware.UpdateCheckRequest, bool) {
	request := firmware.UpdateCheckRequest{
		DeviceID:        firstNonEmptyString(r.URL.Query().Get("device_id"), r.URL.Query().Get("deviceID"), r.URL.Query().Get("device")),
		CurrentFirmware: firstNonEmptyString(r.URL.Query().Get("firmware_id"), r.URL.Query().Get("firmwareID")),
	}
	if version, ok := intFromString(firstNonEmptyString(r.URL.Query().Get("version"), r.URL.Query().Get("current_version"))); ok {
		request.CurrentVersion = version
	} else {
		return request, false
	}

	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") && r.Body != nil {
		var body struct {
			DeviceID        string `json:"device_id"`
			DeviceIDAlt     string `json:"deviceID"`
			Version         int    `json:"version"`
			CurrentVersion  int    `json:"current_version"`
			FirmwareID      string `json:"firmware_id"`
			FirmwareIDAlt   string `json:"firmwareID"`
			CurrentFirmware string `json:"current_firmware"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return firmware.UpdateCheckRequest{}, false
		}
		request.DeviceID = firstNonEmptyString(body.DeviceID, body.DeviceIDAlt, request.DeviceID)
		if body.CurrentVersion != 0 {
			request.CurrentVersion = body.CurrentVersion
		} else if body.Version != 0 {
			request.CurrentVersion = body.Version
		}
		request.CurrentFirmware = firstNonEmptyString(body.CurrentFirmware, body.FirmwareID, body.FirmwareIDAlt, request.CurrentFirmware)
	}

	return request, true
}

func flashJobResponse(job *domain.FlashJob) map[string]any {
	return map[string]any{
		"id":                 job.ID,
		"device_id":          job.DeviceID,
		"product_id":         job.ProductID,
		"firmware_id":        job.FirmwareID,
		"firmware_version":   job.FirmwareVersion,
		"binary_path":        job.BinaryPath,
		"size":               job.Size,
		"sha256":             job.SHA256,
		"chunk_size":         job.ChunkSize,
		"chunk_count":        job.ChunkCount,
		"transferred_chunks": job.Transferred,
		"chunks":             job.Chunks,
		"status":             job.Status,
		"progress":           job.Progress,
		"error":              job.Error,
		"created_at":         job.CreatedAt,
		"updated_at":         job.UpdatedAt,
		"started_at":         job.StartedAt,
		"completed_at":       job.CompletedAt,
	}
}

func firmwareResponse(firmware *domain.ProductFirmware) map[string]any {
	return map[string]any{
		"id":            firmware.ID,
		"product_id":    firmware.ProductID,
		"version":       firmware.Version,
		"title":         firmware.Title,
		"description":   firmware.Description,
		"filename":      firmware.Filename,
		"content_type":  firmware.ContentType,
		"size":          firmware.Size,
		"sha256":        firmware.SHA256,
		"binary_path":   firmware.BinaryPath,
		"release_notes": firmware.ReleaseNotes,
		"released":      firmware.Released,
		"default":       firmware.Default,
		"current":       firmware.Current,
		"created_at":    firmware.CreatedAt,
		"updated_at":    firmware.UpdatedAt,
	}
}
