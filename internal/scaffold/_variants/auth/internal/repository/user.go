package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/owenwexler/gotth-plus/internal/model"
)

// UserRepository stores users and their single-use email tokens.
type UserRepository interface {
	// Create inserts user; ErrConflict when the email is taken.
	Create(ctx context.Context, user *model.User) error
	GetByID(ctx context.Context, id uuid.UUID) (model.User, error)
	// GetByEmail expects an email that is already lowercased and trimmed.
	GetByEmail(ctx context.Context, email string) (model.User, error)
	// SetPassword stores a new hash. With signOutEverywhere it also raises SessionVersion, which ends
	// every existing session of the user.
	SetPassword(ctx context.Context, id uuid.UUID, hash string, signOutEverywhere bool) error
	// SignOutEverywhere raises SessionVersion, which ends every existing session of the user.
	SignOutEverywhere(ctx context.Context, id uuid.UUID) error
	MarkEmailVerified(ctx context.Context, id uuid.UUID, at time.Time) error

	CreateToken(ctx context.Context, token model.AuthToken) error
	// UseToken marks an unused, unexpired token as used and returns it, in one step, so a token
	// can't be used twice even by two requests at once. ErrNotFound otherwise.
	UseToken(ctx context.Context, purpose, tokenHash string, now time.Time) (model.AuthToken, error)
}

type userRepository struct{ db *gorm.DB }

// NewUserRepository creates a UserRepository backed by db.
func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepository{db: db}
}

func (r *userRepository) Create(ctx context.Context, user *model.User) error {
	err := r.db.WithContext(ctx).Create(user).Error
	if err == nil {
		return nil
	}
	// Each driver reports a unique violation differently; if the email exists now, that was it.
	if _, getErr := r.GetByEmail(ctx, user.Email); getErr == nil {
		return ErrConflict
	}
	return err
}

func (r *userRepository) GetByID(ctx context.Context, id uuid.UUID) (model.User, error) {
	return r.first(ctx, "id = ?", id.String())
}

func (r *userRepository) GetByEmail(ctx context.Context, email string) (model.User, error) {
	return r.first(ctx, "email = ?", email)
}

func (r *userRepository) first(ctx context.Context, query string, arg any) (model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where(query, arg).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.User{}, ErrNotFound
	}
	return user, err
}

func (r *userRepository) SetPassword(ctx context.Context, id uuid.UUID, hash string, signOutEverywhere bool) error {
	updates := map[string]any{"password_hash": hash}
	if signOutEverywhere {
		updates["session_version"] = gorm.Expr("session_version + 1")
	}
	return r.updateUser(ctx, id, updates)
}

func (r *userRepository) SignOutEverywhere(ctx context.Context, id uuid.UUID) error {
	return r.updateUser(ctx, id, map[string]any{"session_version": gorm.Expr("session_version + 1")})
}

func (r *userRepository) MarkEmailVerified(ctx context.Context, id uuid.UUID, at time.Time) error {
	return r.updateUser(ctx, id, map[string]any{"email_verified_at": at})
}

func (r *userRepository) updateUser(ctx context.Context, id uuid.UUID, updates map[string]any) error {
	res := r.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", id.String()).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *userRepository) CreateToken(ctx context.Context, token model.AuthToken) error {
	return r.db.WithContext(ctx).Create(&token).Error
}

func (r *userRepository) UseToken(ctx context.Context, purpose, tokenHash string, now time.Time) (model.AuthToken, error) {
	var token model.AuthToken
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.AuthToken{}).
			Where("token_hash = ? AND purpose = ? AND used_at IS NULL AND expires_at > ?", tokenHash, purpose, now).
			Update("used_at", now)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrNotFound
		}
		return tx.Where("token_hash = ?", tokenHash).First(&token).Error
	})
	return token, err
}
