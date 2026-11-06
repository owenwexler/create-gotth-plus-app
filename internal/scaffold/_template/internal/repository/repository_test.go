package repository

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/owenwexler/gotth-plus/internal/database"
	"github.com/owenwexler/gotth-plus/internal/model"
)

func TestPaginate(t *testing.T) {
	db, err := database.ConnectDb(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Example{}); err != nil {
		t.Fatal(err)
	}

	repo := NewExampleRepository(db)
	ctx := context.Background()

	examples := make([]model.Example, 0, ConstantPageSize+2)
	for i := 0; i < ConstantPageSize+2; i++ {
		examples = append(examples, model.Example{ID: uuid.New(), Name: "example"})
	}
	if err := repo.CreateMultiple(ctx, examples); err != nil {
		t.Fatal(err)
	}

	// page <= 0 is treated as the first page
	for page, want := range map[int]int{0: ConstantPageSize, 1: ConstantPageSize, 2: 2, 3: 0} {
		got, err := repo.GetAll(ctx, page)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != want {
			t.Errorf("page %d: %d rows, want %d", page, len(got), want)
		}
	}

	// the page size is clamped to 100, and a size <= 0 falls back to 10
	var rows []model.Example
	if err := db.Scopes(Paginate(1, 0)).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 10 {
		t.Errorf("pageSize 0: %d rows, want 10", len(rows))
	}

	if _, err := repo.GetByID(ctx, uuid.New()); err != ErrNotFound {
		t.Errorf("GetByID(unknown) = %v, want ErrNotFound", err)
	}
}
