package auth

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/owenwexler/gotth-plus/internal/model"
)

// sweepEvery bounds how often a write also deletes expired sessions, so the table can't grow forever
// without a background goroutine.
const sweepEvery = 10 * time.Minute

// Store keeps scs sessions in the database (the sessions table, model.Session), so they survive
// restarts, work across instances, and can be revoked.
type Store struct {
	db        *gorm.DB
	lastSweep atomic.Int64 // unix seconds
}

// NewStore returns a session store on db. The sessions table must exist (model.All).
func NewStore(db *gorm.DB) *Store {
	s := &Store{db: db}
	s.lastSweep.Store(time.Now().Unix())
	return s
}

// FindCtx returns a session's data, or found=false when it doesn't exist or has expired.
func (s *Store) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	var row model.Session
	err := s.db.WithContext(ctx).Where("token = ? AND expiry > ?", token, time.Now()).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return row.Data, true, nil
}

// CommitCtx saves a session.
func (s *Store) CommitCtx(ctx context.Context, token string, b []byte, expiry time.Time) error {
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "token"}},
		DoUpdates: clause.AssignmentColumns([]string{"data", "expiry"}),
	}).Create(&model.Session{Token: token, Data: b, Expiry: expiry}).Error
	if err != nil {
		return err
	}
	s.sweep(ctx)
	return nil
}

// DeleteCtx removes a session.
func (s *Store) DeleteCtx(ctx context.Context, token string) error {
	return s.db.WithContext(ctx).Where("token = ?", token).Delete(&model.Session{}).Error
}

// sweep deletes expired sessions, at most once per sweepEvery.
func (s *Store) sweep(ctx context.Context) {
	now := time.Now()
	last := s.lastSweep.Load()
	if now.Unix()-last < int64(sweepEvery/time.Second) || !s.lastSweep.CompareAndSwap(last, now.Unix()) {
		return
	}
	_ = s.db.WithContext(ctx).Where("expiry <= ?", now).Delete(&model.Session{}).Error
}

// Find, Commit and Delete satisfy scs.Store; scs calls the Ctx versions above when they exist.
func (s *Store) Find(token string) ([]byte, bool, error) {
	return s.FindCtx(context.Background(), token)
}

func (s *Store) Commit(token string, b []byte, expiry time.Time) error {
	return s.CommitCtx(context.Background(), token, b, expiry)
}

func (s *Store) Delete(token string) error {
	return s.DeleteCtx(context.Background(), token)
}
