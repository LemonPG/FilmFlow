package app

import (
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Database wraps the database connection
type Database struct {
	DB *gorm.DB
}

// NewDatabase creates a new database connection
func NewDatabase(dataDir string) (*Database, error) {
	if dataDir == "" {
		dataDir = "."
	}

	dbPath := filepath.Join(dataDir, "FilmFlow.db")

	// Configure GORM
	gormConfig := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent), // Change to Info for debugging
	}

	// Open database connection
	db, err := gorm.Open(sqlite.Open(dbPath), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect database: %w", err)
	}

	// Auto migrate tables
	if err := db.AutoMigrate(models...); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	log.Printf("Database initialized: %s", dbPath)

	return &Database{DB: db}, nil
}

// Close closes the database connection
func (d *Database) Close() error {
	sqlDB, err := d.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// FileStateRepository provides methods for FileState operations
type FileStateRepository struct {
	db *gorm.DB
}

// NewFileStateRepository creates a new FileStateRepository
func NewFileStateRepository(db *gorm.DB) *FileStateRepository {
	return &FileStateRepository{db: db}
}

// GetByFileID retrieves a file state by FileID
func (r *FileStateRepository) GetByFileID(fileID string) (*FileState, error) {
	var state FileState
	err := r.db.Where("file_id = ?", fileID).First(&state).Error
	if err != nil {
		return nil, err
	}
	return &state, nil
}

// Upsert creates or updates a file state
func (r *FileStateRepository) Upsert(state *FileState) error {
	return r.db.Where("file_id = ?", state.FileID).
		Assign(map[string]interface{}{
			"pick_code":       state.PickCode,
			"file_name":       state.FileName,
			"file_path":       state.FilePath,
			"strm_path":       state.StrmPath,
			"url":             state.URL,
			"size":            state.Size,
			"is_dir":          state.IsDir,
			"parent_file_id":  state.ParentFileID,
			"depth":           state.Depth,
			"folder_path":     state.FolderPath,
			"last_scanned_at": state.LastScannedAt,
			"create_time":     state.CreateTime,
			"update_time":     state.UpdateTime,
		}).
		FirstOrCreate(state).Error
}

// DeleteByFileID deletes a file state by FileID
func (r *FileStateRepository) DeleteByFileID(fileID string) error {
	return r.db.Where("file_id = ?", fileID).Delete(&FileState{}).Error
}

// GetAll retrieves all file states
func (r *FileStateRepository) GetAll() ([]FileState, error) {
	var states []FileState
	err := r.db.Find(&states).Error
	return states, err
}

// GetByPath retrieves file states by path prefix
func (r *FileStateRepository) GetByPath(pathPrefix string) ([]FileState, error) {
	var states []FileState
	err := r.db.Where("file_path LIKE ?", pathPrefix+"%").Find(&states).Error
	return states, err
}

// GetByParentFileID retrieves file states by parent file ID
func (r *FileStateRepository) GetByParentFileID(parentFileID string) ([]FileState, error) {
	var states []FileState
	err := r.db.Where("parent_file_id = ?", parentFileID).Find(&states).Error
	return states, err
}

// ScanHistoryRepository provides methods for ScanHistory operations
type ScanHistoryRepository struct {
	db *gorm.DB
}

// NewScanHistoryRepository creates a new ScanHistoryRepository
func NewScanHistoryRepository(db *gorm.DB) *ScanHistoryRepository {
	return &ScanHistoryRepository{db: db}
}

// Create creates a new scan history record
func (r *ScanHistoryRepository) Create(history *ScanHistory) error {
	return r.db.Create(history).Error
}

// Update updates a scan history record
func (r *ScanHistoryRepository) Update(history *ScanHistory) error {
	return r.db.Save(history).Error
}

// GetLatest retrieves the latest scan history
func (r *ScanHistoryRepository) GetLatest() (*ScanHistory, error) {
	var history ScanHistory
	err := r.db.Order("scan_time DESC").First(&history).Error
	if err != nil {
		return nil, err
	}
	return &history, nil
}

// GetRecent retrieves recent scan histories
func (r *ScanHistoryRepository) GetRecent(limit int) ([]ScanHistory, error) {
	var histories []ScanHistory
	err := r.db.Order("scan_time DESC").Limit(limit).Find(&histories).Error
	return histories, err
}

// TaskQueueRepository provides methods for TaskQueue operations
type TaskQueueRepository struct {
	db *gorm.DB
}

// NewTaskQueueRepository creates a new TaskQueueRepository
func NewTaskQueueRepository(db *gorm.DB) *TaskQueueRepository {
	return &TaskQueueRepository{db: db}
}

// Create creates a new task
func (r *TaskQueueRepository) Create(task *TaskQueue) error {
	return r.db.Create(task).Error
}

// GetNextPending retrieves the next pending task
func (r *TaskQueueRepository) GetNextPending() (*TaskQueue, error) {
	var task TaskQueue
	err := r.db.Where("status = ? AND scheduled <= ?", "pending", "now()").
		Order("priority DESC, created_at ASC").
		First(&task).Error
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// UpdateStatus updates task status
func (r *TaskQueueRepository) UpdateStatus(id uint, status, message string) error {
	return r.db.Model(&TaskQueue{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":  status,
			"message": message,
		}).Error
}

// DeleteCompleted deletes completed tasks older than specified duration
func (r *TaskQueueRepository) DeleteCompleted(olderThanHours int) error {
	return r.db.Where("status IN ? AND created_at < datetime('now', '-%d hours')",
		[]string{"completed", "failed"}, olderThanHours).
		Delete(&TaskQueue{}).Error
}

// UserRepository provides methods for User operations
type UserRepository struct {
	db *gorm.DB
}

// NewUserRepository creates a new UserRepository
func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

// GetByUsername retrieves a user by username
func (r *UserRepository) GetByUsername(username string) (*User, error) {
	var user User
	err := r.db.Where("username = ?", username).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// Create creates a new user
func (r *UserRepository) Create(user *User) error {
	return r.db.Create(user).Error
}

// Update updates a user
func (r *UserRepository) Update(user *User) error {
	return r.db.Save(user).Error
}

// GetFirst retrieves the first user (there should be only one)
func (r *UserRepository) GetFirst() (*User, error) {
	var user User
	err := r.db.First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// Count returns the number of users
func (r *UserRepository) Count() (int64, error) {
	var count int64
	err := r.db.Model(&User{}).Count(&count).Error
	return count, err
}

// BehaviorRecordRepository provides methods for BehaviorRecord operations
type BehaviorRecordRepository struct {
	db *gorm.DB
}

// NewBehaviorRecordRepository creates a new BehaviorRecordRepository
func NewBehaviorRecordRepository(db *gorm.DB) *BehaviorRecordRepository {
	return &BehaviorRecordRepository{db: db}
}

// GetByBehaviorID retrieves a behavior record by BehaviorID
func (r *BehaviorRecordRepository) GetByBehaviorID(behaviorID string) (*BehaviorRecord, error) {
	var record BehaviorRecord
	err := r.db.Where("behavior_id = ?", behaviorID).First(&record).Error
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// Create creates a new behavior record
func (r *BehaviorRecordRepository) Create(record *BehaviorRecord) error {
	return r.db.Create(record).Error
}

// Exists checks if a behavior record exists by BehaviorID
func (r *BehaviorRecordRepository) Exists(behaviorID string) (bool, error) {
	var count int64
	err := r.db.Model(&BehaviorRecord{}).Where("behavior_id = ?", behaviorID).Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// DeleteByBehaviorID deletes a behavior record by BehaviorID
func (r *BehaviorRecordRepository) DeleteByBehaviorID(behaviorID string) error {
	return r.db.Where("behavior_id = ?", behaviorID).Delete(&BehaviorRecord{}).Error
}

// DeleteOldRecords deletes behavior records older than specified days
func (r *BehaviorRecordRepository) DeleteOldRecords(days int) error {
	cutoffTime := time.Now().AddDate(0, 0, -days)
	return r.db.Where("processed_at < ?", cutoffTime).Delete(&BehaviorRecord{}).Error
}

// MemorySessionStore provides in-memory session storage
type MemorySessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session // token -> session
	nextID   uint
}

// NewMemorySessionStore creates a new in-memory session store
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{
		sessions: make(map[string]*Session),
		nextID:   1,
	}
}

// Create creates a new session
func (s *MemorySessionStore) Create(session *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Assign ID if not set
	if session.ID == 0 {
		session.ID = s.nextID
		s.nextID++
	}

	session.CreatedAt = time.Now()
	session.UpdatedAt = time.Now()

	s.sessions[session.Token] = session
	return nil
}

// GetByToken retrieves a session by token
func (s *MemorySessionStore) GetByToken(token string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, exists := s.sessions[token]
	if !exists {
		return nil, fmt.Errorf("session not found")
	}

	// Check if session is expired
	if session.ExpiresAt.Before(time.Now()) {
		// Auto-delete expired session
		s.mu.RUnlock()
		s.mu.Lock()
		delete(s.sessions, token)
		s.mu.Unlock()
		s.mu.RLock()
		return nil, fmt.Errorf("session expired")
	}

	return session, nil
}

// UpdateLastActivity updates the last activity time of a session
func (s *MemorySessionStore) UpdateLastActivity(sessionID uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find session by ID
	for _, session := range s.sessions {
		if session.ID == sessionID {
			session.LastActivity = time.Now()
			session.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("session not found")
}

// DeleteByToken deletes a session by token
func (s *MemorySessionStore) DeleteByToken(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, token)
	return nil
}

// DeleteExpired deletes expired sessions
func (s *MemorySessionStore) DeleteExpired() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for token, session := range s.sessions {
		if session.ExpiresAt.Before(now) {
			delete(s.sessions, token)
		}
	}

	return nil
}

// DeleteByUserID deletes all sessions for a user
func (s *MemorySessionStore) DeleteByUserID(userID uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for token, session := range s.sessions {
		if session.UserID == userID {
			delete(s.sessions, token)
		}
	}

	return nil
}

// Cleanup starts a background goroutine to clean up expired sessions periodically
func (s *MemorySessionStore) Cleanup(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			s.DeleteExpired()
		}
	}()
}
