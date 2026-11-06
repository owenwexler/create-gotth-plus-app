package model

import (
	"time"

	"github.com/google/uuid"
)

// Example is a placeholder domain type that shows the model -> repository -> seed path.
// Replace it with your own models.
type Example struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}
