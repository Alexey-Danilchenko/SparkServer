package webhooks

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("webhook record not found")
	ErrConflict = errors.New("webhook record already exists")
)

type Webhook struct {
	ID              string            `json:"id"`
	OwnerID         string            `json:"owner_id,omitempty"`
	Event           string            `json:"event"`
	URL             string            `json:"url"`
	Method          string            `json:"method"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	FailCount       int               `json:"fail_count,omitempty"`
	LastStatus      int               `json:"last_status,omitempty"`
	LastError       string            `json:"last_error,omitempty"`
	LastDeliveredAt *time.Time        `json:"last_delivered_at,omitempty"`
	NextAttemptAt   *time.Time        `json:"next_attempt_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

func (webhook Webhook) GetID() string { return webhook.ID }

type Store interface {
	Create(ctx context.Context, webhook *Webhook) error
	GetByID(ctx context.Context, id string) (*Webhook, error)
	Save(ctx context.Context, webhook *Webhook) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]Webhook, error)
}
