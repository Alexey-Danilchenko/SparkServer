// Package httpapi contains webhook CRUD handlers and response formatting.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"sparkserver/internal/auth"
	"sparkserver/internal/webhooks"
)

func registerWebhookRoutes(router *http.ServeMux, authService *auth.Service, webhookService WebhookService) {
	router.Handle("GET /v1/webhooks", requireAuth(authService, http.HandlerFunc(listWebhooksHandler(webhookService))))
	router.Handle("POST /v1/webhooks", requireAuth(authService, http.HandlerFunc(createWebhookHandler(webhookService))))
	router.Handle("GET /v1/webhooks/{webhookID}", requireAuth(authService, http.HandlerFunc(getWebhookHandler(webhookService))))
	router.Handle("PUT /v1/webhooks/{webhookID}", requireAuth(authService, http.HandlerFunc(updateWebhookHandler(webhookService))))
	router.Handle("DELETE /v1/webhooks/{webhookID}", requireAuth(authService, http.HandlerFunc(deleteWebhookHandler(webhookService))))
}

// WebhookService is the HTTP-facing subset implemented by webhooks.Service.
type WebhookService interface {
	Create(ctx context.Context, request webhooks.Request) (*webhooks.Webhook, error)
	List(ctx context.Context, ownerID string) ([]webhooks.Webhook, error)
	Get(ctx context.Context, ownerID string, id string) (*webhooks.Webhook, error)
	Update(ctx context.Context, ownerID string, id string, request webhooks.Request) (*webhooks.Webhook, error)
	Delete(ctx context.Context, ownerID string, id string) error
}

func listWebhooksHandler(webhookService WebhookService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if webhookService == nil {
			writeError(w, http.StatusServiceUnavailable, "webhooks_unavailable")
			return
		}

		webhooks, err := webhookService.List(r.Context(), userFromContext(r.Context()).ID)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		response := make([]map[string]any, 0, len(webhooks))
		for index := range webhooks {
			response = append(response, webhookResponse(&webhooks[index]))
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func createWebhookHandler(webhookService WebhookService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if webhookService == nil {
			writeError(w, http.StatusServiceUnavailable, "webhooks_unavailable")
			return
		}

		request, ok := webhookRequestFromHTTP(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if request.Event == "" || request.URL == "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		request.OwnerID = userFromContext(r.Context()).ID

		webhook, err := webhookService.Create(r.Context(), request)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, webhookResponse(webhook))
	}
}

func getWebhookHandler(webhookService WebhookService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if webhookService == nil {
			writeError(w, http.StatusServiceUnavailable, "webhooks_unavailable")
			return
		}

		webhook, err := webhookService.Get(r.Context(), userFromContext(r.Context()).ID, r.PathValue("webhookID"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, webhookResponse(webhook))
	}
}

func updateWebhookHandler(webhookService WebhookService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if webhookService == nil {
			writeError(w, http.StatusServiceUnavailable, "webhooks_unavailable")
			return
		}

		request, ok := webhookRequestFromHTTP(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		webhook, err := webhookService.Update(r.Context(), userFromContext(r.Context()).ID, r.PathValue("webhookID"), request)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, webhookResponse(webhook))
	}
}

func deleteWebhookHandler(webhookService WebhookService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if webhookService == nil {
			writeError(w, http.StatusServiceUnavailable, "webhooks_unavailable")
			return
		}

		if err := webhookService.Delete(r.Context(), userFromContext(r.Context()).ID, r.PathValue("webhookID")); err != nil {
			writeServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func webhookRequestFromHTTP(r *http.Request) (webhooks.Request, bool) {
	var request webhooks.Request
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Event   string            `json:"event"`
			URL     string            `json:"url"`
			Method  string            `json:"method"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return request, false
		}
		request.Event = body.Event
		request.URL = body.URL
		request.Method = body.Method
		request.Headers = body.Headers
		request.Body = body.Body
		return request, webhookRequestHasChanges(request)
	}

	if err := r.ParseForm(); err != nil {
		return request, false
	}
	request.Event = r.Form.Get("event")
	request.URL = r.Form.Get("url")
	request.Method = r.Form.Get("method")
	request.Body = r.Form.Get("body")
	return request, webhookRequestHasChanges(request)
}

func webhookRequestHasChanges(request webhooks.Request) bool {
	return request.Event != "" || request.URL != "" || request.Method != "" || request.Headers != nil || request.Body != ""
}

func webhookResponse(webhook *webhooks.Webhook) map[string]any {
	return map[string]any{
		"id":                webhook.ID,
		"owner_id":          webhook.OwnerID,
		"event":             webhook.Event,
		"url":               webhook.URL,
		"method":            webhook.Method,
		"headers":           webhook.Headers,
		"body":              webhook.Body,
		"fail_count":        webhook.FailCount,
		"last_status":       webhook.LastStatus,
		"last_error":        webhook.LastError,
		"last_delivered_at": webhook.LastDeliveredAt,
		"next_attempt_at":   webhook.NextAttemptAt,
		"created_at":        webhook.CreatedAt,
		"updated_at":        webhook.UpdatedAt,
	}
}
