// Package events provides event persistence, SSE fan-out, and webhook sink dispatch.
package events

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"sparkserver/internal/domain"
	"sparkserver/internal/repository"
)

// Filter limits event streams by name prefix, device, or product.
type Filter struct {
	Prefix    string
	DeviceID  string
	ProductID string
}

// Sink receives published events; webhooks implement this interface.
type Sink interface {
	DeliverEvent(ctx context.Context, event domain.Event) error
}

// Service stores events and fans them out to subscribers and sinks.
type Service struct {
	events      repository.EventRepository
	mutex       sync.RWMutex
	nextID      int
	subscribers map[int]subscription
	sinks       []Sink
	clock       func() time.Time
}

type subscription struct {
	filter Filter
	events chan domain.Event
}

// NewService creates an event broker backed by an optional repository.
func NewService(events repository.EventRepository) *Service {
	return &Service{
		events:      events,
		subscribers: make(map[int]subscription),
		clock:       time.Now,
	}
}

// Subscribe returns a buffered stream closed when the caller's context ends.
func (service *Service) Subscribe(ctx context.Context, filter Filter) <-chan domain.Event {
	service.mutex.Lock()
	id := service.nextID
	service.nextID++
	events := make(chan domain.Event, 16)
	service.subscribers[id] = subscription{filter: filter, events: events}
	service.mutex.Unlock()

	// Cleanup of the resources - essentially destructor
	go func() {
		<-ctx.Done()
		service.mutex.Lock()
		delete(service.subscribers, id)
		close(events)
		service.mutex.Unlock()
	}()

	return events
}

func (service *Service) AddSink(sink Sink) {
	if sink == nil {
		return
	}

	service.mutex.Lock()
	defer service.mutex.Unlock()
	service.sinks = append(service.sinks, sink)
}

// Publish assigns metadata, persists the event, and broadcasts it to listeners.
func (service *Service) Publish(ctx context.Context, event *domain.Event) (*domain.Event, error) {
	if event.ID == "" {
		event.ID = newEventID()
	}
	if event.Published.IsZero() {
		event.Published = service.clock().UTC()
	}

	if service.events != nil {
		if err := service.events.Create(ctx, event); err != nil {
			return nil, err
		}
	}

	service.publishToSubscribers(*event)
	service.deliverToSinks(ctx, *event)
	return event, nil
}

func (service *Service) publishToSubscribers(event domain.Event) {
	service.mutex.RLock()
	defer service.mutex.RUnlock()

	for _, subscriber := range service.subscribers {
		if !matches(subscriber.filter, event) {
			continue
		}

		select {
		case subscriber.events <- event:
		default:
		}
	}
}

func (service *Service) deliverToSinks(ctx context.Context, event domain.Event) {
	service.mutex.RLock()
	sinks := append([]Sink(nil), service.sinks...)
	service.mutex.RUnlock()

	for _, sink := range sinks {
		_ = sink.DeliverEvent(ctx, event)
	}
}

func matches(filter Filter, event domain.Event) bool {
	if filter.Prefix != "" && !strings.HasPrefix(event.Name, filter.Prefix) {
		return false
	}
	if filter.DeviceID != "" && event.DeviceID != filter.DeviceID {
		return false
	}
	if filter.ProductID != "" && event.ProductID != filter.ProductID {
		return false
	}
	return true
}

func newEventID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes)
}
