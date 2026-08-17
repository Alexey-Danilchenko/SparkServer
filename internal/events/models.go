package events

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("event record not found")
	ErrConflict = errors.New("event record already exists")
)

type Event struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Data      string    `json:"data,omitempty"`
	DeviceID  string    `json:"device_id,omitempty"`
	ProductID string    `json:"product_id,omitempty"`
	Published time.Time `json:"published"`
}

func (event Event) GetID() string { return event.ID }

type Store interface {
	Create(ctx context.Context, event *Event) error
}
