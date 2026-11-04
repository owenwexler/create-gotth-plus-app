package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/owenwexler/gotth-plus/internal/model"
)

// IdentityRepository stores the links between users and sign-in provider accounts.
type IdentityRepository interface {
	// Get returns the identity for a provider account, or ErrNotFound.
	Get(ctx context.Context, provider, subject string) (model.Identity, error)
	// Create links a provider account to a user; ErrConflict when that account is already linked.
	Create(ctx context.Context, identity model.Identity) error
}

type identityRepository struct{ db *gorm.DB }

// NewIdentityRepository creates an IdentityRepository backed by db.
func NewIdentityRepository(db *gorm.DB) IdentityRepository {
	return &identityRepository{db: db}
}

func (r *identityRepository) Get(ctx context.Context, provider, subject string) (model.Identity, error) {
	// Find with Limit, not First: an unknown account is routine here, and First would log it as an error
	var identity model.Identity
	res := r.db.WithContext(ctx).Where("provider = ? AND subject = ?", provider, subject).Limit(1).Find(&identity)
	if res.Error != nil {
		return model.Identity{}, res.Error
	}
	if res.RowsAffected == 0 {
		return model.Identity{}, ErrNotFound
	}
	return identity, nil
}

func (r *identityRepository) Create(ctx context.Context, identity model.Identity) error {
	err := r.db.WithContext(ctx).Create(&identity).Error
	if err == nil {
		return nil
	}
	// each driver reports a unique violation differently; if the link exists now, that was it
	if _, getErr := r.Get(ctx, identity.Provider, identity.Subject); getErr == nil {
		return ErrConflict
	}
	return err
}
