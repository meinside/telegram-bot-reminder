package database

import "time"

// constants
const (
	DefaultMaxNumTries = 10
)

// Log struct is for logging messages
type Log struct {
	Type    string    `json:"type"`
	Message string    `json:"message"`
	Time    time.Time `json:"time"`
}

// QueueItem is a struct for queue items
type QueueItem struct {
	ID          int64     `json:"id"`
	ChatID      int64     `json:"chat_id"`
	MessageID   int64     `json:"message_id"`
	Message     string    `json:"message"`
	FileID      string    `json:"file_id,omitempty"`
	FileType    FileType  `json:"file_type,omitempty"`
	EnqueuedOn  time.Time `json:"enqueued_on"`
	FireOn      time.Time `json:"fire_on"`
	DeliveredOn time.Time `json:"delivered_on,omitempty"`
	NumTries    int       `json:"num_tries"`
}

// TemporaryMessage is a struct for temporary message for handling inline queries
type TemporaryMessage struct {
	ID        int64     `json:"id"`
	ChatID    int64     `json:"chat_id"`
	MessageID int64     `json:"message_id"`
	Message   string    `json:"message"`
	FileID    string    `json:"file_id,omitempty"`
	FileType  FileType  `json:"file_type,omitempty"`
	SavedOn   time.Time `json:"saved_on"`
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
