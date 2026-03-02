package app

import (
	"time"
)

// FileState stores the state of processed files
type FileState struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	FileID        string    `gorm:"uniqueIndex;not null" json:"fileId"`
	PickCode      string    `gorm:"not null" json:"pickCode"`
	FileName      string    `gorm:"not null" json:"fileName"`
	FilePath      string    `gorm:"not null" json:"filePath"`
	StrmPath      string    `gorm:"not null" json:"strmPath"`
	URL           string    `gorm:"not null" json:"url"`
	Size          int64     `json:"size"`
	IsDir         bool      `json:"isDir"`
	ParentFileID  string    `json:"parentFileId"`  // 父文件夹的FileID
	Depth         int       `json:"depth"`         // 文件夹/文件深度（从根目录开始）
	FolderPath    string    `json:"folderPath"`    // 文件夹路径（对于文件是其所在文件夹路径，对于文件夹是其自身路径）
	LastScannedAt time.Time `json:"lastScannedAt"` // 最后扫描时间（主要用于文件夹）
	CreateTime    time.Time `json:"createTime"`    // 网盘文件的创建时间
	UpdateTime    time.Time `json:"updateTime"`    // 网盘文件的更新时间
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// ScanHistory stores scan history records
type ScanHistory struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ScanTime  time.Time `gorm:"not null" json:"scanTime"`
	Created   int       `json:"created"`
	Updated   int       `json:"updated"`
	Skipped   int       `json:"skipped"`
	Total     int       `json:"total"`
	Duration  string    `json:"duration"`
	Status    string    `json:"status"` // "success", "failed", "running"
	Message   string    `json:"message"`
	RootCID   string    `gorm:"not null" json:"rootCid"`
	MaxDepth  int       `json:"maxDepth"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// TaskQueue stores background tasks
type TaskQueue struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TaskType  string    `gorm:"not null" json:"taskType"` // "scan", "sync"
	Status    string    `gorm:"not null" json:"status"`   // "pending", "running", "completed", "failed"
	Priority  int       `json:"priority"`
	Payload   string    `json:"payload"` // JSON payload
	Message   string    `json:"message"`
	Retry     int       `json:"retry"`
	MaxRetry  int       `json:"maxRetry"`
	Scheduled time.Time `json:"scheduled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// User stores user authentication information
type User struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Username     string    `gorm:"uniqueIndex;not null;default:'admin'" json:"username"`
	PasswordHash string    `gorm:"not null" json:"-"`                     // 不暴露给JSON
	IsSetup      bool      `gorm:"not null;default:false" json:"isSetup"` // 是否已设置密码
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Session stores user session information (in-memory only)
type Session struct {
	ID           uint      `json:"id"`
	UserID       uint      `json:"userId"`
	Token        string    `json:"-"` // 64字符的十六进制令牌
	ExpiresAt    time.Time `json:"expiresAt"`
	LastActivity time.Time `json:"lastActivity"`
	UserAgent    string    `json:"userAgent"`
	IPAddress    string    `json:"ipAddress"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`

	// 关联
	User User `json:"-"`
}

// BehaviorRecord stores processed behavior records to avoid duplicate processing
type BehaviorRecord struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	BehaviorID    string    `gorm:"uniqueIndex;not null" json:"behaviorId"` // 行为记录的唯一ID
	OperationType string    `gorm:"not null" json:"operationType"`          // 操作类型：new_folder, copy_folder, folder_rename, move_file, delete_file
	CID           string    `gorm:"not null" json:"cid"`                    // 受影响的目录ID
	ProcessedAt   time.Time `gorm:"not null" json:"processedAt"`            // 处理时间
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Database models slice (Session is NOT included as it's stored in memory only)
var models = []interface{}{
	&FileState{},
	&ScanHistory{},
	&TaskQueue{},
	&User{},
	&BehaviorRecord{},
}

// TableName returns the table name for FileState
func (FileState) TableName() string {
	return "file_states"
}

// TableName returns the table name for ScanHistory
func (ScanHistory) TableName() string {
	return "scan_histories"
}

// TableName returns the table name for TaskQueue
func (TaskQueue) TableName() string {
	return "task_queues"
}

// TableName returns the table name for User
func (User) TableName() string {
	return "users"
}

// TableName returns the table name for BehaviorRecord
func (BehaviorRecord) TableName() string {
	return "behavior_records"
}
