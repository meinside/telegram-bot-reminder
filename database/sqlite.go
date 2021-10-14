package database

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	// for sqlite3
	_ "github.com/mattn/go-sqlite3"
)

// SQLiteDatabase is an interface for sqlite3 database
type SQLiteDatabase struct {
	db *sql.DB
	sync.RWMutex
}

var _sqlite *SQLiteDatabase = nil

// OpenSQLiteDB opens a connection to a sqlite file.
func OpenSQLiteDB(filepath string) (*SQLiteDatabase, error) {
	if _sqlite == nil {
		var db *sql.DB
		var err error

		if db, err = sql.Open("sqlite3", filepath); err != nil {
			return nil, fmt.Errorf("failed to open database: %s", err)
		}

		_sqlite = &SQLiteDatabase{
			db: db,
		}

		// logs table
		if _, err = db.Exec(`create table if not exists logs(
				id integer primary key autoincrement,
				type text default null,
				message text not null,
				time integer default (strftime('%s', 'now'))
			)`); err != nil {
			return nil, fmt.Errorf("failed to create logs table: %s", err)
		}

		// temporary messages table
		if _, err = db.Exec(`create table if not exists temp_messages(
				id integer primary key autoincrement,
				chat_id integer not null,
				message_id integer not null,
				message text not null,
				file_id text default '',
				file_type text default '',
				saved_on integer default (strftime('%s', 'now'))
			)`); err != nil {
			return nil, fmt.Errorf("failed to create temp_messages table: %s", err)
		}
		if _, err = db.Exec(`create index if not exists idx_temp_messages1 on temp_messages(
				chat_id, message_id
			)`); err != nil {
			return nil, fmt.Errorf("failed to create index idx_temp_messages1: %s", err)
		}

		// queue table
		if _, err = db.Exec(`create table if not exists queue(
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
			return nil, fmt.Errorf("failed to create queue table: %s", err)
		}
		if _, err = db.Exec(`create index if not exists idx_queue1 on queue(
				chat_id, delivered_on
			)`); err != nil {
			return nil, fmt.Errorf("failed to create index idx_queue1: %s", err)
		}
		if _, err = db.Exec(`create index if not exists idx_queue2 on queue(
				enqueued_on, delivered_on
			)`); err != nil {
			return nil, fmt.Errorf("failed to create index idx_queue2: %s", err)
		}
		if _, err = db.Exec(`create index if not exists idx_queue3 on queue(
				enqueued_on, delivered_on, num_tries
			)`); err != nil {
			return nil, fmt.Errorf("failed to create index idx_queue3: %s", err)
		}
		if _, err = db.Exec(`create index if not exists idx_queue4 on queue(
				chat_id, delivered_on, enqueued_on
			)`); err != nil {
			return nil, fmt.Errorf("failed to create index idx_queue4: %s", err)
		}
		if _, err = db.Exec(`create index if not exists idx_queue5 on queue(
				enqueued_on, delivered_on, num_tries, fire_on
			)`); err != nil {
			return nil, fmt.Errorf("failed to create index idx_queue5: %s", err)
		}
	}

	return _sqlite, nil
}

// CloseDB closes database
func (d *SQLiteDatabase) CloseDB() {
	if d.db != nil {
		d.db.Close()
	}

	_sqlite = nil
}

func (d *SQLiteDatabase) saveLog(typ, msg string) (err error) {
	d.Lock()
	defer d.Unlock()

	var stmt *sql.Stmt
	if stmt, err = d.db.Prepare(`insert into logs(type, message) values(?, ?)`); err != nil {
		log.Printf("* failed to prepare a statement: %s", err)
	} else {
		defer stmt.Close()
		if _, err = stmt.Exec(typ, msg); err != nil {
			log.Printf("* failed to save log into local database: %s", err)
		}
	}

	return err
}

// Log logs a message
func (d *SQLiteDatabase) Log(msg string) {
	if err := d.saveLog("log", msg); err != nil {
		log.Printf("failed to save log message: %s", err)
	}
}

// LogError logs an error message
func (d *SQLiteDatabase) LogError(msg string) {
	if err := d.saveLog("err", msg); err != nil {
		log.Printf("failed to save error message: %s", err)
	}
}

// GetLogs fetches `latestN` number of latest logs
func (d *SQLiteDatabase) GetLogs(latestN int) (logs []Log, err error) {
	d.RLock()
	defer d.RUnlock()

	var stmt *sql.Stmt
	if stmt, err = d.db.Prepare(`select type, message, time from logs order by id desc limit ?`); err != nil {
		log.Printf("* failed to prepare a statement: %s", err)
	} else {
		defer stmt.Close()

		var rows *sql.Rows
		if rows, err = stmt.Query(latestN); err != nil {
			log.Printf("* failed to select logs from local database: %s", err)
		} else {
			defer rows.Close()

			var typ, msg string
			var tm int64
			for rows.Next() {
				if err = rows.Scan(&typ, &msg, &tm); err == nil {
					logs = append(logs, Log{
						Type:    typ,
						Message: msg,
						Time:    time.Unix(tm, 0),
					})
				} else {
					log.Printf("* failed to scan row: %s", err)
				}
			}
		}
	}

	return logs, err
}

// SaveTemporaryMessage saves a temporary message
func (d *SQLiteDatabase) SaveTemporaryMessage(chatID int64, messageID int64, message, fileID string, fileType FileType) (result bool, err error) {
	d.Lock()
	defer d.Unlock()

	var stmt *sql.Stmt
	if stmt, err = d.db.Prepare(`insert or ignore into temp_messages(chat_id, message_id, message, file_id, file_type) values(?, ?, ?, ?, ?)`); err != nil {
		log.Printf("* failed to prepare a statement: %s", err)
	} else {
		defer stmt.Close()

		if _, err = stmt.Exec(chatID, messageID, message, fileID, fileType); err != nil {
			log.Printf("* failed to save temporary message into local database: %s", err)
		} else {
			result = true
		}
	}

	return result, err
}

// LoadTemporaryMessage retrieves a temporary message
func (d *SQLiteDatabase) LoadTemporaryMessage(chatID int64, messageID int64) (result TemporaryMessage, err error) {
	d.RLock()
	defer d.RUnlock()

	var stmt *sql.Stmt
	if stmt, err = d.db.Prepare(`select 
		id,
		chat_id, 
		message_id,
		message, 
		file_id,
		file_type,
		ifnull(saved_on, 0) as saved_on
		from temp_messages
		where chat_id = ? and message_id = ?`); err != nil {
		log.Printf("* failed to prepare a statement: %s", err)
	} else {
		defer stmt.Close()

		var rows *sql.Rows
		if rows, err = stmt.Query(chatID, messageID); err != nil {
			log.Printf("* failed to select temporary message from local database: %s", err)
		} else {
			defer rows.Close()

			var id, chatID, messageID int64
			var message, fileID string
			var fileType FileType
			var savedOn int64
			if rows.Next() {
				if err = rows.Scan(&id, &chatID, &messageID, &message, &fileID, &fileType, &savedOn); err == nil {
					result = TemporaryMessage{
						ID:        id,
						ChatID:    chatID,
						MessageID: messageID,
						Message:   message,
						FileID:    fileID,
						FileType:  fileType,
						SavedOn:   time.Unix(savedOn, 0),
					}
				} else {
					log.Printf("* failed to scan row: %s", err)
				}
			} else {
				err = fmt.Errorf("no temporary message for chat id = %d, message id = %d", chatID, messageID)
			}
		}
	}

	return result, err
}

// DeleteTemporaryMessage deletes given temporary message
func (d *SQLiteDatabase) DeleteTemporaryMessage(chatID int64, messageID int64) (result bool, err error) {
	d.Lock()
	defer d.Unlock()

	var stmt *sql.Stmt
	if stmt, err = d.db.Prepare(`delete from temp_messages where chat_id = ? and message_id = ?`); err != nil {
		log.Printf("* failed to prepare a statement: %s", err)
	} else {
		defer stmt.Close()

		if _, err = stmt.Exec(chatID, messageID); err != nil {
			log.Printf("* failed to delete temporary message from local database: %s", err)
		} else {
			result = true
		}
	}

	return result, err
}

// Enqueue enques given message
func (d *SQLiteDatabase) Enqueue(chatID int64, messageID int64, message, fileID string, fileType FileType, fireOn time.Time) (result bool, err error) {
	d.Lock()
	defer d.Unlock()

	var stmt *sql.Stmt
	if stmt, err = d.db.Prepare(`insert or ignore into queue(chat_id, message_id, message, file_id, file_type, fire_on) values(?, ?, ?, ?, ?, ?)`); err != nil {
		log.Printf("* failed to prepare a statement: %s", err)
	} else {
		defer stmt.Close()

		if _, err = stmt.Exec(chatID, messageID, message, fileID, fileType, fireOn.Unix()); err != nil {
			log.Printf("* failed to save queue item into local database: %s", err)
		} else {
			result = true
		}
	}

	return result, err
}

// DeliverableQueueItems fetches all items from the queue which need to be delivered right now.
func (d *SQLiteDatabase) DeliverableQueueItems(maxNumTries int) (queue []QueueItem, err error) {
	if maxNumTries <= 0 {
		maxNumTries = DefaultMaxNumTries
	}

	d.RLock()
	defer d.RUnlock()

	var stmt *sql.Stmt
	if stmt, err = d.db.Prepare(`select 
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
		log.Printf("* failed to prepare a statement: %s", err)
	} else {
		defer stmt.Close()

		var rows *sql.Rows
		if rows, err = stmt.Query(maxNumTries, time.Now().Unix()); err != nil {
			log.Printf("* failed to select queue items from local database: %s", err)
		} else {
			defer rows.Close()

			var id, chatID, messageID int64
			var message, fileID string
			var fileType FileType
			var enqueuedOn, fireOn, deliveredOn int64
			for rows.Next() {
				if err = rows.Scan(&id, &chatID, &messageID, &message, &fileID, &fileType, &enqueuedOn, &fireOn, &deliveredOn); err == nil {
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
				} else {
					log.Printf("* failed to scan row: %s", err)
				}
			}
		}
	}

	return queue, err
}

// UndeliveredQueueItems fetches all undelivered items from the queue.
func (d *SQLiteDatabase) UndeliveredQueueItems(chatID int64) (queue []QueueItem, err error) {
	d.RLock()
	defer d.RUnlock()

	var stmt *sql.Stmt
	if stmt, err = d.db.Prepare(`select 
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
		log.Printf("* failed to prepare a statement: %s", err)
	} else {
		defer stmt.Close()

		var rows *sql.Rows
		if rows, err = stmt.Query(chatID); err != nil {
			log.Printf("* failed to select queue items from local database: %s", err)
		} else {
			defer rows.Close()

			var id, chatID, messageID int64
			var message, fileID string
			var fileType FileType
			var enqueuedOn, fireOn, deliveredOn int64
			for rows.Next() {
				if err = rows.Scan(&id, &chatID, &messageID, &message, &fileID, &fileType, &enqueuedOn, &fireOn, &deliveredOn); err == nil {
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
				} else {
					log.Printf("* failed to scan row: %s", err)
				}
			}
		}
	}

	return queue, err
}

// GetQueueItem fetches a queue item
func (d *SQLiteDatabase) GetQueueItem(chatID, queueID int64) (queueItem QueueItem, err error) {
	d.RLock()
	defer d.RUnlock()

	var stmt *sql.Stmt
	if stmt, err = d.db.Prepare(`select 
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
		where id = ? and chat_id = ?`); err != nil {
		log.Printf("* failed to prepare a statement: %s", err)
	} else {
		defer stmt.Close()

		var rows *sql.Rows
		if rows, err = stmt.Query(queueID, chatID); err != nil {
			log.Printf("* failed to select a queue item from local database: %s", err)
		} else {
			defer rows.Close()

			var id, chatID, messageID int64
			var message, fileID string
			var fileType FileType
			var enqueuedOn, fireOn, deliveredOn int64
			if rows.Next() {
				if err = rows.Scan(&id, &chatID, &messageID, &message, &fileID, &fileType, &enqueuedOn, &fireOn, &deliveredOn); err == nil {
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
					log.Printf("* failed to scan row: %s", err)
				}
			}

			log.Printf("* failed to select a queue item with id = %d, chat_id = %d from local database", id, chatID)

			err = fmt.Errorf("no such queue item with id = %d, chat_id = %d", id, chatID)
		}
	}

	return QueueItem{}, err
}

// DeleteQueueItem deletes a queue item
func (d *SQLiteDatabase) DeleteQueueItem(chatID, queueID int64) (result bool, err error) {
	d.Lock()
	defer d.Unlock()

	var stmt *sql.Stmt
	if stmt, err = d.db.Prepare(`delete from queue where id = ? and chat_id = ?`); err != nil {
		log.Printf("* failed to prepare a statement: %s", err)
	} else {
		defer stmt.Close()

		if _, err = stmt.Exec(queueID, chatID); err != nil {
			log.Printf("* failed to delete queue item from local database: %s", err)
		} else {
			result = true
		}
	}

	return result, err
}

// IncreaseNumTries increases the number of tries of a queue item
func (d *SQLiteDatabase) IncreaseNumTries(chatID, queueID int64) (result bool, err error) {
	d.Lock()
	defer d.Unlock()

	var stmt *sql.Stmt
	if stmt, err = d.db.Prepare(`update queue set num_tries = num_tries + 1 where id = ? and chat_id = ?`); err != nil {
		log.Printf("* failed to prepare a statement: %s", err)
	} else {
		defer stmt.Close()

		var res sql.Result
		if res, err = stmt.Exec(queueID, chatID); err != nil {
			log.Printf("* failed to increase num_tries in local database: %s", err)
		} else {
			if num, _ := res.RowsAffected(); num <= 0 {
				log.Printf("* failed to increase num_tires for id: %d, chat_id: %d", queueID, chatID)
			} else {
				result = true
			}
		}
	}

	return result, err
}

// MarkQueueItemAsDelivered makes a queue item as delivered
func (d *SQLiteDatabase) MarkQueueItemAsDelivered(chatID, queueID int64) (result bool, err error) {
	d.Lock()
	defer d.Unlock()

	var stmt *sql.Stmt
	if stmt, err = d.db.Prepare(`update queue set delivered_on = ? where id = ? and chat_id = ?`); err != nil {
		log.Printf("* failed to prepare a statement: %s", err)
	} else {
		defer stmt.Close()

		now := time.Now()

		var res sql.Result
		if res, err = stmt.Exec(now.Unix(), queueID, chatID); err != nil {
			log.Printf("* failed to mark delivered_on in local database: %s", err)
		} else {
			if num, _ := res.RowsAffected(); num <= 0 {
				log.Printf("* failed to mark delivered_on for id: %d, chat_id: %d", queueID, chatID)
			} else {
				result = true
			}
		}
	}

	return result, err
}
