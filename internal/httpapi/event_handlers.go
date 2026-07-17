// Package httpapi contains Particle-style event publishing and SSE stream handlers.
package httpapi

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"

	"sparkserver/internal/domain"
	"sparkserver/internal/events"
)

func pingHandler(w http.ResponseWriter, r *http.Request) {
	payload := map[string]any{}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&payload)
	}
	payload["serverPayload"] = rand.Float64()
	writeJSON(w, http.StatusOK, payload)
}

func streamEventsFromPathHandler(eventService *events.Service, deviceID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		streamEvents(w, r, eventService, events.Filter{
			Prefix:   r.PathValue("prefix"),
			DeviceID: deviceID,
		})
	}
}

func streamProductEventsHandler(
	eventService   *events.Service,
	productService ProductService,
) http.HandlerFunc {
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
		streamEvents(w, r, eventService, events.Filter{
			Prefix:    r.PathValue("prefix"),
			ProductID: product.ID,
		})
	}
}

func streamDeviceEventsHandler(eventService *events.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		streamEvents(w, r, eventService, events.Filter{
			Prefix:   r.PathValue("prefix"),
			DeviceID: r.PathValue("deviceIDorName"),
		})
	}
}

func streamEventsHandler(eventService *events.Service, filter events.Filter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		streamEvents(w, r, eventService, filter)
	}
}

func streamEvents(
	w            http.ResponseWriter,
	r            *http.Request,
	eventService *events.Service,
	filter       events.Filter,
) {
	if eventService == nil {
		writeError(w, http.StatusServiceUnavailable, "events_unavailable")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unavailable")
		return
	}

	// Particle clients expect a long-lived server-sent event stream.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events := eventService.Subscribe(r.Context(), filter)
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := writeSSE(w, event); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func publishEventHandler(eventService *events.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if eventService == nil {
			writeError(w, http.StatusServiceUnavailable, "events_unavailable")
			return
		}

		event, ok := eventFromRequest(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		published, err := eventService.Publish(r.Context(), event)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"id":        published.ID,
			"name":      published.Name,
			"published": published.Published,
		})
	}
}

func eventFromRequest(r *http.Request) (*domain.Event, bool) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Name     string `json:"name"`
			Event    string `json:"event"`
			Data     string `json:"data"`
			DeviceID string `json:"deviceID"`
			CoreID   string `json:"coreid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, false
		}

		name := body.Name
		if name == "" {
			name = body.Event
		}
		deviceID := body.DeviceID
		if deviceID == "" {
			deviceID = body.CoreID
		}
		return &domain.Event{Name: name, Data: body.Data, DeviceID: deviceID}, name != ""
	}

	if err := r.ParseForm(); err != nil {
		return nil, false
	}

	name := r.Form.Get("name")
	if name == "" {
		name = r.Form.Get("event")
	}
	deviceID := r.Form.Get("deviceID")
	if deviceID == "" {
		deviceID = r.Form.Get("coreid")
	}

	return &domain.Event{Name: name, Data: r.Form.Get("data"), DeviceID: deviceID}, name != ""
}

func writeSSE(w http.ResponseWriter, event domain.Event) error {
	payload := map[string]any{
		"name":         event.Name,
		"data":         event.Data,
		"coreid":       event.DeviceID,
		"published_at": event.Published,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "event: %s\n", event.Name); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	return nil
}
