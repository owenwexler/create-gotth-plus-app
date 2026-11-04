package repository

import (
	"errors"

	"gorm.io/gorm"
)

var ConstantPageSize = 10

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("record not found")

// ErrConflict is returned when a record with the same unique key already exists.
var ErrConflict = errors.New("record already exists")

// ErrInvalidInput is returned when the caller supplies a logically inconsistent request.
var ErrInvalidInput = errors.New("invalid input")

// ErrHasDependents is returned when deleting a record that is still referenced by other records.
var ErrHasDependents = errors.New("record has dependents")

// Paginate acts as a GORM Scope
// example usage: db.Scopes(repository.Paginate(2, 10)).Find(&examples)
func Paginate(page, pageSize int) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if page <= 0 {
			page = 1
		}
		switch {
		case pageSize > 100:
			pageSize = 100 // protects against massive database strain
		case pageSize <= 0:
			pageSize = 10
		}

		offset := (page - 1) * pageSize
		return db.Offset(offset).Limit(pageSize)
	}
}
