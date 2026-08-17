package auth

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("auth record not found")
	ErrConflict = errors.New("auth record already exists")
)

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	Scopes       []string  `json:"scopes,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (user User) GetID() string { return user.ID }

type AccessToken struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Scopes    []string  `json:"scopes,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

func (token AccessToken) GetID() string { return token.Token }

type UserStore interface {
	Create(ctx context.Context, user *User) error
	GetByID(ctx context.Context, id string) (*User, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
}

type AccessTokenStore interface {
	Create(ctx context.Context, token *AccessToken) error
	GetByID(ctx context.Context, id string) (*AccessToken, error)
	GetByUserID(ctx context.Context, userID string) ([]AccessToken, error)
	Delete(ctx context.Context, id string) error
}
