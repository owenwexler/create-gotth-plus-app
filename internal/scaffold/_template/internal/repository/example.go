package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/owenwexler/gotth-plus/internal/model"
)

// ExampleRepository defines the interface for example DB operations.
type ExampleRepository interface {
	GetAll(ctx context.Context, page int) ([]model.Example, error)
	GetByID(ctx context.Context, id uuid.UUID) (model.Example, error)
	Create(ctx context.Context, example model.Example) error
	CreateMultiple(ctx context.Context, examples []model.Example) error
	Count(ctx context.Context) (int, error)
}

type exampleRepository struct{ db *gorm.DB }

// NewExampleRepository creates a new ExampleRepository backed by db.
func NewExampleRepository(db *gorm.DB) ExampleRepository {
	return &exampleRepository{db: db}
}

func (r *exampleRepository) GetAll(ctx context.Context, page int) ([]model.Example, error) {
	var examples []model.Example

	if err := r.db.WithContext(ctx).Scopes(Paginate(page, ConstantPageSize)).Order("created_at DESC").Find(&examples).Error; err != nil {
		return nil, err
	}

	return examples, nil
}

func (r *exampleRepository) GetByID(ctx context.Context, id uuid.UUID) (model.Example, error) {
	var example model.Example

	err := r.db.WithContext(ctx).First(&example, "id = ?", id.String()).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.Example{}, ErrNotFound
	}

	return example, err
}

func (r *exampleRepository) Create(ctx context.Context, example model.Example) error {
	return r.db.WithContext(ctx).Create(&example).Error
}

// CreateMultiple bulk-inserts examples, skipping any whose ID already exists.
func (r *exampleRepository) CreateMultiple(ctx context.Context, examples []model.Example) error {
	if len(examples) == 0 {
		return nil
	}

	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&examples).Error
}

func (r *exampleRepository) Count(ctx context.Context) (int, error) {
	var count int64

	if err := r.db.WithContext(ctx).Model(&model.Example{}).Count(&count).Error; err != nil {
		return 0, err
	}

	return int(count), nil
}
