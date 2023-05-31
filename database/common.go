package database

import (
	"fmt"
	"log"
	"sync"
	"time"

	"gorm.io/gorm"
)

var _database *Database = nil

// constants
const (
	DefaultMaxNumTries = 10
)

// Database is an interface for handling database
type Database struct {
	db *gorm.DB
	sync.RWMutex
}

// Log struct is for logging messages
type Log struct {
	gorm.Model

	Type    string
	Message string
}

// QueueItem is a struct for queue items
type QueueItem struct {
	gorm.Model

	ID          int64
	ChatID      int64 `gorm:"index:idx_queue1;index:idx_queue4"`
	MessageID   int64
	Message     string
	FileID      *string
	FileType    *FileType
	EnqueuedOn  time.Time  `gorm:"index:idx_queue2;index:idx_queue3;index:idx_queue4;index:idx_queue5"`
	FireOn      time.Time  `gorm:"index:idx_queue5"`
	DeliveredOn *time.Time `gorm:"index:idx_queue1;index:idx_queue2;index:idx_queue3;index:idx_queue4;index:idx_queue5"`
	NumTries    int        `gorm:"index:idx_queue3;index:idx_queue5"`
}

// TemporaryMessage is a struct for temporary message for handling inline queries
type TemporaryMessage struct {
	gorm.Model

	ID        int64
	ChatID    int64 `gorm:"index:idx_temp_messages1"`
	MessageID int64 `gorm:"index:idx_temp_messages1"`
	Message   string
	FileID    *string
	FileType  *FileType
	SavedOn   time.Time
}

// FileType is a type of message (file) type
type FileType string

// filetype constants
const (
	FileTypeDocument FileType = "document"
	FileTypeAudio    FileType = "audio"
	FileTypePhoto    FileType = "photo"
	FileTypeSticker  FileType = "sticker"
	FileTypeVideo    FileType = "video"
	FileTypeVoice    FileType = "voice"
)

// migrate things
func (d *Database) doMigrations() {
	// auto migration
	if err := d.db.AutoMigrate(&Log{}); err != nil {
		log.Printf("* failed to migrate database for `Log`: %s", err)
	}
	if err := d.db.AutoMigrate(&QueueItem{}); err != nil {
		log.Printf("* failed to migrate database for `QueueItem`: %s", err)
	}
	if err := d.db.AutoMigrate(&TemporaryMessage{}); err != nil {
		log.Printf("* failed to migrate database for `TemporaryMessage`: %s", err)
	}
}

func (d *Database) saveLog(typ, msg string) (err error) {
	d.Lock()
	defer d.Unlock()

	res := d.db.Create(&Log{
		Type:    typ,
		Message: msg,
	})

	return res.Error
}

// Log logs a message
func (d *Database) Log(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)

	if err := d.saveLog("log", msg); err != nil {
		log.Printf("failed to save log message: %s", err)
	}
}

// LogError logs an error message
func (d *Database) LogError(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)

	if err := d.saveLog("err", msg); err != nil {
		log.Printf("failed to save error message: %s", err)
	}
}

// GetLogs fetches `latestN` number of latest logs
func (d *Database) GetLogs(latestN int) (logs []Log, err error) {
	d.RLock()
	defer d.RUnlock()

	result := d.db.Order("id desc").Limit(latestN).Find(&logs)

	return logs, result.Error
}

// SaveTemporaryMessage saves a temporary message
func (d *Database) SaveTemporaryMessage(chatID int64, messageID int64, message string, fileID *string, fileType *FileType) (result bool, err error) {
	d.Lock()
	defer d.Unlock()

	res := d.db.Create(&TemporaryMessage{
		ChatID:    chatID,
		MessageID: messageID,
		Message:   message,
		FileID:    fileID,
		FileType:  fileType,
		SavedOn:   time.Now(),
	})

	return res.RowsAffected > 0, res.Error
}

// LoadTemporaryMessage retrieves a temporary message
func (d *Database) LoadTemporaryMessage(chatID, messageID int64) (result TemporaryMessage, err error) {
	d.RLock()
	defer d.RUnlock()

	res := d.db.Where("chat_id = ? and message_id = ?", chatID, messageID).First(&result)

	return result, res.Error
}

// DeleteTemporaryMessage deletes given temporary message
func (d *Database) DeleteTemporaryMessage(chatID int64, messageID int64) (result bool, err error) {
	d.Lock()
	defer d.Unlock()

	res := d.db.Where("chat_id = ? and message_id = ?", chatID, messageID).Delete(&TemporaryMessage{ChatID: chatID, MessageID: messageID})

	return res.RowsAffected > 0, res.Error
}

// Enqueue enques given message
func (d *Database) Enqueue(chatID int64, messageID int64, message string, fileID *string, fileType *FileType, fireOn time.Time) (result bool, err error) {
	d.Lock()
	defer d.Unlock()

	res := d.db.Save(&QueueItem{
		ChatID:    chatID,
		MessageID: messageID,
		Message:   message,
		FileID:    fileID,
		FileType:  fileType,
		FireOn:    fireOn,
	})

	return res.RowsAffected > 0, res.Error
}

// DeliverableQueueItems fetches all items from the queue which need to be delivered right now.
func (d *Database) DeliverableQueueItems(maxNumTries int) (result []QueueItem, err error) {
	if maxNumTries <= 0 {
		maxNumTries = DefaultMaxNumTries
	}

	d.RLock()
	defer d.RUnlock()

	res := d.db.Order("enqueued_on desc").Where("delivered_on is null and num_tries < ? and fire_on <= ?", maxNumTries, time.Now()).Find(&result)

	return result, res.Error
}

// UndeliveredQueueItems fetches all undelivered items from the queue.
func (d *Database) UndeliveredQueueItems(chatID int64) (result []QueueItem, err error) {
	d.RLock()
	defer d.RUnlock()

	res := d.db.Order("fire_on asc").Where("chat_id = ? and delivered_on is null", chatID).Find(&result)

	return result, res.Error
}

// GetQueueItem fetches a queue item
func (d *Database) GetQueueItem(chatID, queueID int64) (result QueueItem, err error) {
	d.RLock()
	defer d.RUnlock()

	res := d.db.Where("id = ? and chat_id = ?", queueID, chatID).First(&result)

	return result, res.Error
}

// DeleteQueueItem deletes a queue item
func (d *Database) DeleteQueueItem(chatID, queueID int64) (result bool, err error) {
	d.Lock()
	defer d.Unlock()

	res := d.db.Where("id = ? and chat_id = ?", queueID, chatID).Delete(&QueueItem{})

	return res.RowsAffected > 0, res.Error
}

// IncreaseNumTries increases the number of tries of a queue item
func (d *Database) IncreaseNumTries(chatID, queueID int64) (result bool, err error) {
	d.Lock()
	defer d.Unlock()

	res := d.db.Model(&QueueItem{}).Where("id = ? and chat_id = ?", queueID, chatID).Update("num_tries", gorm.Expr("num_tries + 1"))

	return res.RowsAffected > 0, res.Error
}

// MarkQueueItemAsDelivered makes a queue item as delivered
func (d *Database) MarkQueueItemAsDelivered(chatID, queueID int64) (result bool, err error) {
	d.Lock()
	defer d.Unlock()

	res := d.db.Model(&QueueItem{}).Where("id = ? and chat_id = ?", queueID, chatID).Update("delivered_on", time.Now())

	return res.RowsAffected > 0, res.Error
}
