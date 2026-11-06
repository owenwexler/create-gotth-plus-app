package seed

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/owenwexler/gotth-plus/internal/model"
	"github.com/owenwexler/gotth-plus/internal/repository"
)

// seedFile is read relative to the working directory (the project root).
const seedFile = "seedData.json"

type jsonExample struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SeedHandler handles the /seed route.
type SeedHandler struct {
	db          *gorm.DB
	exampleRepo repository.ExampleRepository
}

// NewSeedHandler creates a new SeedHandler.
func NewSeedHandler(db *gorm.DB, exampleRepo repository.ExampleRepository) *SeedHandler {
	return &SeedHandler{db: db, exampleRepo: exampleRepo}
}

// Seed reads the seed JSON file and bulk-inserts data. Rows that already exist are left alone,
// so it is safe to run more than once.
func (h *SeedHandler) Seed(r *http.Request) error {
	if err := h.db.AutoMigrate(&model.Example{}); err != nil {
		return fmt.Errorf("seed: migrating schema: %w", err)
	}

	// examples
	seedData, err := os.ReadFile(seedFile)
	if err != nil {
		return fmt.Errorf("seed: reading json: %w", err)
	}
	var rawExamples []jsonExample
	if err := json.Unmarshal(seedData, &rawExamples); err != nil {
		return fmt.Errorf("seed: parsing %s: %w", seedFile, err)
	}
	examples := make([]model.Example, len(rawExamples))
	// Space created_at one second apart so the created_at DESC sort is stable.
	// The first example in the file is the newest, so lists render in file order.
	now := time.Now()
	for i, je := range rawExamples {
		createdAt := now.Add(-time.Duration(i) * time.Second)
		id, err := uuid.Parse(je.ID)
		if err != nil {
			return fmt.Errorf("seed: invalid example id %q: %w", je.ID, err)
		}
		examples[i] = model.Example{
			ID:        id,
			Name:      je.Name,
			CreatedAt: createdAt,
			UpdatedAt: createdAt,
			DeletedAt: nil,
		}
	}
	if err := h.exampleRepo.CreateMultiple(r.Context(), examples); err != nil {
		return fmt.Errorf("seed: inserting examples: %w", err)
	}

	slog.InfoContext(r.Context(), "seed complete", "examples", len(examples))
	return nil
}
