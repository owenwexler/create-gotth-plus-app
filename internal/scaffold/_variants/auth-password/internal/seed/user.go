package seed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/owenwexler/gotth-plus/internal/auth"
	"github.com/owenwexler/gotth-plus/internal/model"
	"github.com/owenwexler/gotth-plus/internal/repository"
)

// DevUserEmail is the development account the seed route creates.
const DevUserEmail = "dev@example.com"

// DevUser creates the development account (confirmed, with password) unless it exists. Without a
// password (SeedUserPassword in .env) it does nothing. Development only, like the seed route.
func DevUser(ctx context.Context, users repository.UserRepository, password string) error {
	if password == "" {
		return nil
	}
	if _, err := users.GetByEmail(ctx, DevUserEmail); err == nil {
		return nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if err := auth.CheckPasswordLength(password); err != nil {
		return fmt.Errorf("SeedUserPassword: %w", err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	now := time.Now()
	user := model.User{ID: uuid.New(), Email: DevUserEmail, PasswordHash: hash, EmailVerifiedAt: &now, SessionVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err := users.Create(ctx, &user); err != nil && !errors.Is(err, repository.ErrConflict) {
		return err
	}
	slog.InfoContext(ctx, "seeded dev user", "email", DevUserEmail)
	return nil
}
