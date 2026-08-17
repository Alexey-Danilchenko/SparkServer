// Package auth implements the Particle-compatible password grant and bearer tokens.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

// ErrInvalidCredentials hides whether the username or password was incorrect.
var ErrInvalidCredentials = errors.New("invalid credentials")

// Clock allows tests to control token expiry without sleeping.
type Clock func() time.Time

// Service coordinates user creation, login, token storage, and bearer auth checks.
type Service struct {
	users         UserStore
	tokens        AccessTokenStore
	tokenLifetime time.Duration
	clock         Clock
}

// NewService binds auth behavior to repository implementations.
func NewService(
	users UserStore,
	tokens AccessTokenStore,
	tokenLifetime time.Duration,
) *Service {
	return &Service{
		users:         users,
		tokens:        tokens,
		tokenLifetime: tokenLifetime,
		clock:         time.Now,
	}
}

// EnsureDefaultAdmin creates the fallback local admin account when no user exists.
func (service *Service) EnsureDefaultAdmin(
	ctx context.Context,
	username string,
	password string,
) error {
	if _, err := service.users.GetByUsername(ctx, username); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}

	passwordHash, err := HashPassword(password)
	if err != nil {
		return err
	}

	now := service.clock().UTC()
	user := User{
		ID:           username,
		Username:     username,
		PasswordHash: passwordHash,
		Scopes:       []string{"*"},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	return service.users.Create(ctx, &user)
}

// Login implements the password-grant flow used by Spark/Particle-compatible clients.
func (service *Service) Login(
	ctx context.Context,
	username string,
	password string,
) (*AccessToken, error) {
	user, err := service.users.GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	if !VerifyPassword(user.PasswordHash, password) {
		return nil, ErrInvalidCredentials
	}

	now := service.clock().UTC()
	token := AccessToken{
		Token:     newToken(),
		UserID:    user.ID,
		Username:  user.Username,
		Scopes:    user.Scopes,
		ExpiresAt: now.Add(service.tokenLifetime),
		CreatedAt: now,
	}

	if err := service.tokens.Create(ctx, &token); err != nil {
		return nil, err
	}

	return &token, nil
}

// CreateUser registers a new local account with full local-cloud scope.
func (service *Service) CreateUser(
	ctx context.Context,
	username string,
	password string,
) (*User, error) {
	if _, err := service.users.GetByUsername(ctx, username); err == nil {
		return nil, ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	passwordHash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}

	now := service.clock().UTC()
	user := User{
		ID:           username,
		Username:     username,
		PasswordHash: passwordHash,
		Scopes:       []string{"*"},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := service.users.Create(ctx, &user); err != nil {
		return nil, err
	}

	return &user, nil
}

// AuthenticateToken resolves a bearer token into the current user and token record.
func (service *Service) AuthenticateToken(
	ctx context.Context,
	tokenValue string,
) (*User, *AccessToken, error) {
	token, err := service.tokens.GetByID(ctx, tokenValue)
	if err != nil {
		return nil, nil, err
	}

	if service.clock().UTC().After(token.ExpiresAt) {
		return nil, nil, ErrNotFound
	}

	user, err := service.users.GetByID(ctx, token.UserID)
	if err != nil {
		return nil, nil, err
	}

	return user, token, nil
}

func (service *Service) ListTokens(
	ctx context.Context,
	userID string,
) ([]AccessToken, error) {
	return service.tokens.GetByUserID(ctx, userID)
}

func (service *Service) DeleteToken(ctx context.Context, tokenValue string) error {
	return service.tokens.Delete(ctx, tokenValue)
}

func newToken() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes)
}
