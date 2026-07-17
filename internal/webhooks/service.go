// Package webhooks implements event-triggered outbound HTTP webhook delivery.
package webhooks

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"sparkserver/internal/domain"
	"sparkserver/internal/repository"
)

// Request is the normalized create/update input accepted by HTTP handlers.
type Request struct {
	OwnerID string
	Event   string
	URL     string
	Method  string
	Headers map[string]string
	Body    string
}

// Service stores webhook definitions and delivers matching events over HTTP.
type Service struct {
	webhooks repository.WebhookRepository
	client   *http.Client
	clock    func() time.Time
}

// NewService creates a webhook manager with a conservative HTTP timeout.
func NewService(webhooks repository.WebhookRepository) *Service {
	return &Service{
		webhooks: webhooks,
		client:   &http.Client{Timeout: 5 * time.Second},
		clock:    time.Now,
	}
}

func (service *Service) SetHTTPClient(client *http.Client) {
	if client != nil {
		service.client = client
	}
}

func (service *Service) Create(ctx context.Context, request Request) (*domain.Webhook, error) {
	if request.OwnerID == "" || request.Event == "" || request.URL == "" {
		return nil, repository.ErrNotFound
	}

	method := strings.ToUpper(request.Method)
	if method == "" {
		method = http.MethodPost
	}

	now := service.clock().UTC()
	webhook := &domain.Webhook{
		ID:        newWebhookID(),
		OwnerID:   request.OwnerID,
		Event:     request.Event,
		URL:       request.URL,
		Method:    method,
		Headers:   copyHeaders(request.Headers),
		Body:      request.Body,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if service.webhooks == nil {
		return webhook, nil
	}
	return webhook, service.webhooks.Create(ctx, webhook)
}

func (service *Service) List(ctx context.Context, ownerID string) ([]domain.Webhook, error) {
	if service.webhooks == nil {
		return []domain.Webhook{}, nil
	}
	webhooks, err := service.webhooks.List(ctx)
	if err != nil {
		return nil, err
	}

	matches := make([]domain.Webhook, 0)
	for _, webhook := range webhooks {
		if webhook.OwnerID == ownerID {
			matches = append(matches, webhook)
		}
	}
	sort.Slice(matches, func(left int, right int) bool {
		return matches[left].CreatedAt.Before(matches[right].CreatedAt)
	})
	return matches, nil
}

func (service *Service) Get(
	ctx     context.Context,
	ownerID string,
	id      string,
) (*domain.Webhook, error) {
	if id == "" || service.webhooks == nil {
		return nil, repository.ErrNotFound
	}
	webhook, err := service.webhooks.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if webhook.OwnerID != ownerID {
		return nil, repository.ErrNotFound
	}
	return webhook, nil
}

func (service *Service) Update(
	ctx     context.Context,
	ownerID string,
	id      string,
	request Request,
) (*domain.Webhook, error) {
	webhook, err := service.Get(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}

	if request.Event != "" {
		webhook.Event = request.Event
	}
	if request.URL != "" {
		webhook.URL = request.URL
	}
	if request.Method != "" {
		webhook.Method = strings.ToUpper(request.Method)
	}
	if request.Headers != nil {
		webhook.Headers = copyHeaders(request.Headers)
	}
	if request.Body != "" {
		webhook.Body = request.Body
	}
	webhook.FailCount = 0
	webhook.LastStatus = 0
	webhook.LastError = ""
	webhook.NextAttemptAt = nil
	webhook.UpdatedAt = service.clock().UTC()
	return webhook, service.webhooks.Save(ctx, webhook)
}

func (service *Service) Delete(ctx context.Context, ownerID string, id string) error {
	if _, err := service.Get(ctx, ownerID, id); err != nil {
		return err
	}
	return service.webhooks.Delete(ctx, id)
}

// DeliverEvent implements events.Sink and applies matching plus retry backoff.
func (service *Service) DeliverEvent(ctx context.Context, event domain.Event) error {
	if service.webhooks == nil || service.client == nil {
		return nil
	}

	webhooks, err := service.webhooks.List(ctx)
	if err != nil {
		return err
	}

	var deliveryErr error
	for index := range webhooks {
		if !matches(webhooks[index], event) {
			continue
		}
		if !service.shouldAttempt(&webhooks[index]) {
			continue
		}
		if err := service.deliver(ctx, &webhooks[index], event); err != nil {
			deliveryErr = errors.Join(deliveryErr, err)
		}
	}
	return deliveryErr
}

func (service *Service) deliver(
	ctx     context.Context,
	webhook *domain.Webhook,
	event   domain.Event,
) error {
	body, contentType, err := bodyFor(webhook, event)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, webhook.Method, webhook.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for name, value := range webhook.Headers {
		if name != "" {
			req.Header.Set(name, value)
		}
	}

	response, err := service.client.Do(req)
	if err != nil {
		service.recordFailure(ctx, webhook, 0, err.Error())
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode >= 400 {
		err := fmt.Errorf("webhook %s returned status %d", webhook.ID, response.StatusCode)
		service.recordFailure(ctx, webhook, response.StatusCode, err.Error())
		return err
	}
	service.recordSuccess(ctx, webhook, response.StatusCode)
	return nil
}

func (service *Service) shouldAttempt(webhook *domain.Webhook) bool {
	if webhook.NextAttemptAt == nil {
		return true
	}
	return !service.clock().UTC().Before(*webhook.NextAttemptAt)
}

func (service *Service) recordSuccess(ctx context.Context, webhook *domain.Webhook, status int) {
	now := service.clock().UTC()
	webhook.FailCount = 0
	webhook.LastStatus = status
	webhook.LastError = ""
	webhook.LastDeliveredAt = &now
	webhook.NextAttemptAt = nil
	webhook.UpdatedAt = now
	if service.webhooks != nil {
		_ = service.webhooks.Save(ctx, webhook)
	}
}

func (service *Service) recordFailure(
	ctx     context.Context,
	webhook *domain.Webhook,
	status  int,
	message string,
) {
	now := service.clock().UTC()
	webhook.FailCount++
	webhook.LastStatus = status
	webhook.LastError = message
	next := now.Add(backoffFor(webhook.FailCount))
	webhook.NextAttemptAt = &next
	webhook.UpdatedAt = now
	if service.webhooks != nil {
		_ = service.webhooks.Save(ctx, webhook)
	}
}

func matches(webhook domain.Webhook, event domain.Event) bool {
	if webhook.Event == "*" || webhook.Event == event.Name {
		return true
	}
	prefix, ok := strings.CutSuffix(webhook.Event, "*")
	return ok && strings.HasPrefix(event.Name, prefix)
}

func bodyFor(webhook *domain.Webhook, event domain.Event) ([]byte, string, error) {
	if webhook.Body != "" {
		return []byte(expandTemplate(webhook.Body, event)), "application/json", nil
	}

	body, err := json.Marshal(map[string]any{
		"event":        event.Name,
		"name":         event.Name,
		"data":         event.Data,
		"coreid":       event.DeviceID,
		"device_id":    event.DeviceID,
		"product_id":   event.ProductID,
		"published_at": event.Published,
	})
	return body, "application/json", err
}

func expandTemplate(template string, event domain.Event) string {
	replacements := map[string]string{
		"{{event}}":        event.Name,
		"{{name}}":         event.Name,
		"{{data}}":         event.Data,
		"{{coreid}}":       event.DeviceID,
		"{{device_id}}":    event.DeviceID,
		"{{product_id}}":   event.ProductID,
		"{{published_at}}": event.Published.Format(time.RFC3339Nano),
	}
	for placeholder, value := range replacements {
		template = strings.ReplaceAll(template, placeholder, value)
	}
	return template
}

func backoffFor(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	delay := time.Minute
	for index := 1; index < failures; index++ {
		delay *= 2
		if delay >= time.Hour {
			return time.Hour
		}
	}
	return delay
}

func copyHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	copied := make(map[string]string, len(headers))
	for name, value := range headers {
		copied[name] = value
	}
	return copied
}

func newWebhookID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes)
}
