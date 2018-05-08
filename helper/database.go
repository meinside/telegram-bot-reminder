package helper

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const (
	DefaultMaxNumTries = 10
)

type Database struct {
	db *sql.DB
	sync.RWMutex
}

type Log struct {
	Type    string    `json:"type"`
	Message string    `json:"message"`
	Time    time.Time `json:"time"`
}

type QueueItem struct {
	ID          int64     `json:"id"`
	ChatID      int64     `json:"chat_id"`
	MessageID   int       `json:"message_id"`
	Message     string    `json:"message"`
	FileID      string    `json:"file_id,omitempty"`
	FileType    FileType  `json:"file_type,omitempty"`
	EnqueuedOn  time.Time `json:"enqueued_on"`
	FireOn      time.Time `json:"fire_on"`
	DeliveredOn time.Time `json:"delivered_on,omitempty"`
	NumTries    int       `json:"num_tries"`
}

type FileType string

const (
	FileTypeDocument FileType = "document"
	FileTypeAudio    FileType = "audio"
	FileTypePhoto    FileType = "photo"
	FileTypeSticker  FileType = "sticker"
	FileTypeVideo    FileType = "video"
	FileTypeVoice    FileType = "voice"
)

var _db *Database = nil

func OpenDb(filepath string) *Database {
	if _db == nil {
		if db, err := sql.Open("sqlite3", filepath); err != nil {
			panic("Failed to open database: " + err.Error())
		} else {
			_db = &Database{
				db: db,
			}

			// logs table
			if _, err := db.Exec(`create table if not exists logs(
				id integer primary key autoincrement,
				type text default null,
				message text not null,
				time integer default (strftime('%s', 'now'))
			)`); err != nil {
				panic("Failed to create logs table: " + err.Error())
			}

			// queue table
			if _, err := db.Exec(`create table if not exists queue(
				id integer primary key autoincrement,
				chat_id integer not null,
				message_id integer not null,
				message text not null,
				file_id text default '',
				file_type text default '',
				enqueued_on integer default (strftime('%s', 'now')),
				fire_on integer not null,
				delivered_on integer default null,
				num_tries integer default 0
			)`); err != nil {
				panic("Failed to create queue table: " + err.Error())
			}
			if _, err := db.Exec(`create index if not exists idx_queue1 on queue(
				chat_id, delivered_on
			)`); err != nil {
				panic("Failed to create idx_queue1: " + err.Error())
			}
			if _, err := db.Exec(`create index if not exists idx_queue2 on queue(
				enqueued_on, delivered_on
			)`); err != nil {
				panic("Failed to create idx_queue2: " + err.Error())
			}
			if _, err := db.Exec(`create index if not exists idx_queue3 on queue(
				enqueued_on, delivered_on, num_tries
			)`); err != nil {
				panic("Failed to create idx_queue3: " + err.Error())
			}
			if _, err := db.Exec(`create index if not exists idx_queue4 on queue(
				chat_id, delivered_on, enqueued_on
			)`); err != nil {
				panic("Failed to create idx_queue4: " + err.Error())
			}
			if _, err := db.Exec(`create index if not exists idx_queue5 on queue(
				enqueued_on, delivered_on, num_tries, fire_on
			)`); err != nil {
				panic("Failed to create idx_queue5: " + err.Error())
			}
		}
	}

	return _db
}

func CloseDb() {
	if _db != nil {
		_db.db.Close()
		_db = nil
	}
}

func (d *Database) saveLog(typ, msg string) {
	d.Lock()

	if stmt, err := d.db.Prepare(`insert into logs(type, message) values(?, ?)`); err != nil {
		log.Printf("*** Failed to prepare a statement: %s", err.Error())
	} else {
		defer stmt.Close()
		if _, err = stmt.Exec(typ, msg); err != nil {
			log.Printf("*** Failed to save log into local database: %s", err.Error())
		}
	}

	d.Unlock()
}

func (d *Database) Log(msg string) {
	d.saveLog("log", msg)
}

func (d *Database) LogError(msg string) {
	d.saveLog("err", msg)
}

func (d *Database) GetLogs(latestN int) []Log {
	logs := []Log{}

	d.RLock()

	if stmt, err := d.db.Prepare(`select type, message, time from logs order by id desc limit ?`); err != nil {
		log.Printf("*** Failed to prepare a statement: %s", err.Error())
	} else {
		defer stmt.Close()

		if rows, err := stmt.Query(latestN); err != nil {
			log.Printf("*** Failed to select logs from local database: %s", err.Error())
		} else {
			defer rows.Close()

			var typ, msg string
			var tm int64
			for rows.Next() {
				rows.Scan(&typ, &msg, &tm)

				logs = append(logs, Log{
					Type:    typ,
					Message: msg,
					Time:    time.Unix(tm, 0),
				})
			}
		}
	}

	d.RUnlock()

	return logs
}

func (d *Database) Enqueue(chatID int64, messageID int, message, fileID string, fileType FileType, fireOn time.Time) bool {
	result := false

	d.Lock()

	if stmt, err := d.db.Prepare(`insert or ignore into queue(chat_id, message_id, message, file_id, file_type, fire_on) values(?, ?, ?, ?, ?, ?)`); err != nil {
		log.Printf("*** Failed to prepare a statement: %s", err.Error())
	} else {
		defer stmt.Close()

		if _, err = stmt.Exec(chatID, messageID, message, fileID, fileType, fireOn.Unix()); err != nil {
			log.Printf("*** Failed to save queue item into local database: %s", err.Error())
		} else {
			result = true
		}
	}

	d.Unlock()

	return result
}

// DeliverableQueueItems fetches all items from the queue which need to be delivered right now.
func (d *Database) DeliverableQueueItems(maxNumTries int) []QueueItem {
	queue := []QueueItem{}
	if maxNumTries <= 0 {
		maxNumTries = DefaultMaxNumTries
	}

	d.RLock()

	if stmt, err := d.db.Prepare(`select 
		id,
		chat_id, 
		message_id,
		message, 
		file_id,
		file_type,
		enqueued_on,
		fire_on,
		ifnull(delivered_on, 0) as delivered_on
		from queue
		where delivered_on is null and num_tries < ? and fire_on <= ?
		order by enqueued_on desc`); err != nil {
		log.Printf("*** Failed to prepare a statement: %s", err.Error())
	} else {
		defer stmt.Close()

		if rows, err := stmt.Query(maxNumTries, time.Now().Unix()); err != nil {
			log.Printf("*** Failed to select queue items from local database: %s", err.Error())
		} else {
			defer rows.Close()

			var id, chatID int64
			var messageID int
			var message, fileID string
			var fileType FileType
			var enqueuedOn, fireOn, deliveredOn int64
			for rows.Next() {
				rows.Scan(&id, &chatID, &messageID, &message, &fileID, &fileType, &enqueuedOn, &fireOn, &deliveredOn)

				queue = append(queue, QueueItem{
					ID:          id,
					ChatID:      chatID,
					MessageID:   messageID,
					Message:     message,
					FileID:      fileID,
					FileType:    fileType,
					EnqueuedOn:  time.Unix(enqueuedOn, 0),
					FireOn:      time.Unix(fireOn, 0),
					DeliveredOn: time.Unix(deliveredOn, 0),
				})
			}
		}
	}

	d.RUnlock()

	return queue
}

// UndeliveredQueueItems fetches all undelivered items from the queue.
func (d *Database) UndeliveredQueueItems(chatID int64) []QueueItem {
	queue := []QueueItem{}

	d.RLock()

	if stmt, err := d.db.Prepare(`select 
		id,
		chat_id, 
		message_id,
		message, 
		file_id,
		file_type,
		enqueued_on,
		fire_on,
		ifnull(delivered_on, 0) as delivered_on
		from queue
		where chat_id = ? and delivered_on is null
		order by fire_on asc`); err != nil {
		log.Printf("*** Failed to prepare a statement: %s", err.Error())
	} else {
		defer stmt.Close()

		if rows, err := stmt.Query(chatID); err != nil {
			log.Printf("*** Failed to select queue items from local database: %s", err.Error())
		} else {
			defer rows.Close()

			var id, chatID int64
			var messageID int
			var message, fileID string
			var fileType FileType
			var enqueuedOn, fireOn, deliveredOn int64
			for rows.Next() {
				rows.Scan(&id, &chatID, &messageID, &message, &fileID, &fileType, &enqueuedOn, &fireOn, &deliveredOn)

				queue = append(queue, QueueItem{
					ID:          id,
					ChatID:      chatID,
					MessageID:   messageID,
					Message:     message,
					FileID:      fileID,
					FileType:    fileType,
					EnqueuedOn:  time.Unix(enqueuedOn, 0),
					FireOn:      time.Unix(fireOn, 0),
					DeliveredOn: time.Unix(deliveredOn, 0),
				})
			}
		}
	}

	d.RUnlock()

	return queue
}

func (d *Database) GetQueueItem(chatID, queueID int64) (QueueItem, error) {
	d.RLock()
	defer d.RUnlock()

	var stmt *sql.Stmt
	var err error = nil

	if stmt, err = d.db.Prepare(`select 
		id,
		chat_id, 
		message_id,
		message, 
		file_id,
		file_type,
		enqueued_on,
		fire_on,
		delivered_on
		from queue
		where id = ? and chat_id = ?`); err != nil {
		log.Printf("*** Failed to prepare a statement: %s", err.Error())
	} else {
		defer stmt.Close()

		var rows *sql.Rows
		if rows, err = stmt.Query(queueID, chatID); err != nil {
			log.Printf("*** Failed to select a queue item from local database: %s", err.Error())
		} else {
			defer rows.Close()

			var id, chatID int64
			var messageID int
			var message, fileID string
			var fileType FileType
			var enqueuedOn, fireOn, deliveredOn int64
			if rows.Next() {
				rows.Scan(&id, &chatID, &messageID, &message, &fileID, &fileType, &enqueuedOn, &fireOn, &deliveredOn)

				return QueueItem{
					ID:          id,
					ChatID:      chatID,
					MessageID:   messageID,
					Message:     message,
					FileID:      fileID,
					FileType:    fileType,
					EnqueuedOn:  time.Unix(enqueuedOn, 0),
					FireOn:      time.Unix(fireOn, 0),
					DeliveredOn: time.Unix(deliveredOn, 0),
				}, nil
			} else {
				log.Printf("*** Failed to select a queue item with id = %d, chat_id = %d from local database", id, chatID)

				err = fmt.Errorf("no such queue item with id = %d, chat_id = %d", id, chatID)
			}
		}
	}

	return QueueItem{}, err
}

func (d *Database) DeleteQueueItem(chatID, queueID int64) bool {
	result := false

	d.Lock()

	if stmt, err := d.db.Prepare(`delete from queue where id = ? and chat_id = ?`); err != nil {
		log.Printf("*** Failed to prepare a statement: %s", err.Error())
	} else {
		defer stmt.Close()
		if _, err = stmt.Exec(queueID, chatID); err != nil {
			log.Printf("*** Failed to delete queue item from local database: %s", err.Error())
		} else {
			result = true
		}
	}

	d.Unlock()

	return result
}

func (d *Database) IncreaseNumTries(chatID, queueID int64) bool {
	result := false

	d.Lock()

	if stmt, err := d.db.Prepare(`update queue set num_tries = num_tries + 1 where id = ? and chat_id = ?`); err != nil {
		log.Printf("*** Failed to prepare a statement: %s", err.Error())
	} else {
		defer stmt.Close()

		var res sql.Result
		if res, err = stmt.Exec(queueID, chatID); err != nil {
			log.Printf("*** Failed to increase num_tries in local database: %s", err.Error())
		} else {
			if num, _ := res.RowsAffected(); num <= 0 {
				log.Printf("*** Failed to increase num_tires for id: %d, chat_id: %d", queueID, chatID)
			} else {
				result = true
			}
		}
	}

	d.Unlock()

	return result
}

func (d *Database) MarkQueueItemAsDelivered(chatID, queueID int64) bool {
	result := false

	d.Lock()

	if stmt, err := d.db.Prepare(`update queue set delivered_on = ? where id = ? and chat_id = ?`); err != nil {
		log.Printf("*** Failed to prepare a statement: %s", err.Error())
	} else {
		defer stmt.Close()

		now := time.Now()

		var res sql.Result
		if res, err = stmt.Exec(now.Unix(), queueID, chatID); err != nil {
			log.Printf("*** Failed to mark delivered_on in local database: %s", err.Error())
		} else {
			if num, _ := res.RowsAffected(); num <= 0 {
				log.Printf("*** Failed to mark delivered_on for id: %d, chat_id: %d", queueID, chatID)
			} else {
				result = true
			}
		}
	}

	d.Unlock()

	return result
}
