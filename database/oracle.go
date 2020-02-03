package database

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/godror/godror"
)

// constants
const (
	tablePrefix = "reminder_"
)

// OracleDatabase is an interface for Oracle Database
type OracleDatabase struct {
	db *sql.DB
}

var _oracle *OracleDatabase = nil

// OpenOracleDB opens a connection to an Oracle database with given id, password, and connection string.
func OpenOracleDB(id, passwd, sid string) (*OracleDatabase, error) {
	if _oracle == nil {
		connParams := godror.ConnectionParams{
			Username:             id,
			Password:             passwd,
			SID:                  sid,
			MinSessions:          1,
			MaxSessions:          4,
			PoolIncrement:        1,
			StandaloneConnection: false,
			WaitTimeout:          10 * time.Second,
			MaxLifeTime:          5 * time.Minute,
			SessionTimeout:       30 * time.Second,
			ConnClass:            "POOLED",
			EnableEvents:         true,
		}

		db, err := sql.Open("godror", connParams.StringWithPassword())

		if err != nil {
			log.Printf("failed to connect to oracle database: %s", err)
			return nil, err
		}

		_oracle = &OracleDatabase{
			db: db,
		}

		// create table: `PREFIX_logs`
		if _, err := db.Exec(fmt.Sprintf(`create table %slogs(
				id NUMBER GENERATED ALWAYS AS IDENTITY,
				type NVARCHAR2(16) default null,
				message NVARCHAR2(256) not null,
				time DATE default sysdate not null
			)`, tablePrefix)); err != nil {
			log.Printf("(ignorable) failed to create table `%slogs`: %s", tablePrefix, err)
		}

		// create table: `PREFIX_queue`
		if _, err := db.Exec(fmt.Sprintf(`create table %squeue(
				id NUMBER GENERATED ALWAYS AS IDENTITY,
				chat_id NUMBER not null,
				message_id NUMBER not null,
				message NVARCHAR2(256) not null,
				file_id NVARCHAR2(128) default '',
				file_type NVARCHAR2(32) default '',
				enqueued_on DATE default sysdate not null,
				fire_on DATE not null,
				delivered_on DATE default null,
				num_tries NUMBER default 0
			)`, tablePrefix)); err != nil {
			log.Printf("(ignorable) failed to create table `%squeue`: %s", tablePrefix, err)
		}
		if _, err := db.Exec(fmt.Sprintf(`create index idx_queue1 on %squeue(
				chat_id, delivered_on
			)`, tablePrefix)); err != nil {
			log.Printf("(ignorable) failed to create index `idx_queue1`: %s", err)
		}
		if _, err := db.Exec(fmt.Sprintf(`create index idx_queue2 on %squeue(
				enqueued_on, delivered_on
			)`, tablePrefix)); err != nil {
			log.Printf("(ignorable) failed to create index `idx_queue2`: %s", err)
		}
		if _, err := db.Exec(fmt.Sprintf(`create index idx_queue3 on %squeue(
				enqueued_on, delivered_on, num_tries
			)`, tablePrefix)); err != nil {
			log.Printf("(ignorable) failed to create index `idx_queue3`: %s", err)
		}
		if _, err := db.Exec(fmt.Sprintf(`create index idx_queue4 on %squeue(
				chat_id, delivered_on, enqueued_on
			)`, tablePrefix)); err != nil {
			log.Printf("(ignorable) failed to create index `idx_queue4`: %s", err)
		}
		if _, err := db.Exec(fmt.Sprintf(`create index idx_queue5 on %squeue(
				enqueued_on, delivered_on, num_tries, fire_on
			)`, tablePrefix)); err != nil {
			log.Printf("(ignorable) failed to create index `idx_queue5`: %s", err)
		}
	}

	return _oracle, nil
}

func (d *OracleDatabase) saveLog(typ, msg string) {
	if tx, err := d.db.Begin(); err == nil {
		if stmt, err := d.db.Prepare(fmt.Sprintf(`insert into %slogs(type, message) values(:1, :2)`, tablePrefix)); err != nil {
			log.Printf("* failed to prepare a statement: %s", err)
		} else {
			defer stmt.Close()

			if _, err = stmt.Exec(typ, msg); err != nil {
				log.Printf("* failed to save log into oracle database: %s", err)
			}
		}

		defer tx.Commit()
	} else {
		log.Printf("failed to begin transaction: %s", err)
	}
}

// Log logs a message
func (d *OracleDatabase) Log(msg string) {
	d.saveLog("log", msg)
}

// LogError logs an error message
func (d *OracleDatabase) LogError(msg string) {
	d.saveLog("err", msg)
}

// GetLogs fetches `latestN` number of latest logs
func (d *OracleDatabase) GetLogs(latestN int) []Log {
	logs := []Log{}

	if tx, err := d.db.Begin(); err == nil {
		if stmt, err := d.db.Prepare(fmt.Sprintf(`select type, message, time from %slogs order by id desc limit :1`, tablePrefix)); err != nil {
			log.Printf("* failed to prepare a statement: %s", err)
		} else {
			defer stmt.Close()

			if rows, err := stmt.Query(latestN); err != nil {
				log.Printf("* failed to select logs from oracle database: %s", err)
			} else {
				defer rows.Close()

				var typ, msg string
				var tm time.Time
				for rows.Next() {
					rows.Scan(&typ, &msg, &tm)

					logs = append(logs, Log{
						Type:    typ,
						Message: msg,
						Time:    tm,
					})
				}
			}
		}

		defer tx.Commit()
	} else {
		log.Printf("failed to begin transaction: %s", err)
	}

	return logs
}

// Enqueue enques given message
func (d *OracleDatabase) Enqueue(chatID int64, messageID int, message, fileID string, fileType FileType, fireOn time.Time) bool {
	result := false

	if tx, err := d.db.Begin(); err == nil {
		if stmt, err := d.db.Prepare(fmt.Sprintf(`insert into %squeue(chat_id, message_id, message, file_id, file_type, fire_on) values(:1, :2, :3, :4, :5, :6)`, tablePrefix)); err != nil {
			log.Printf("* failed to prepare a statement: %s", err)
		} else {
			defer stmt.Close()

			if _, err = stmt.Exec(chatID, messageID, message, fileID, string(fileType), fireOn); err != nil {
				log.Printf("* failed to save queue item into oracle database: %s", err)
			} else {
				result = true
			}
		}

		defer tx.Commit()
	} else {
		log.Printf("failed to begin transaction: %s", err)
	}

	return result
}

// DeliverableQueueItems fetches all items from the queue which need to be delivered right now.
func (d *OracleDatabase) DeliverableQueueItems(maxNumTries int) []QueueItem {
	queue := []QueueItem{}
	if maxNumTries <= 0 {
		maxNumTries = DefaultMaxNumTries
	}

	if tx, err := d.db.Begin(); err == nil {
		if stmt, err := d.db.Prepare(fmt.Sprintf(`select 
			id,
			chat_id, 
			message_id,
			message, 
			file_id,
			file_type,
			enqueued_on,
			fire_on,
			delivered_on
			from %squeue
			where delivered_on is null and num_tries < :1 and fire_on <= :2
			order by fire_on`, tablePrefix)); err != nil {
			log.Printf("* failed to prepare a statement: %s", err)
		} else {
			defer stmt.Close()

			if rows, err := stmt.Query(maxNumTries, time.Now()); err != nil {
				log.Printf("* failed to select queue items from oracle database: %s", err)
			} else {
				defer rows.Close()

				var id, chatID int64
				var messageID int
				var message, fileID string
				var fileType FileType
				var enqueuedOn, fireOn, deliveredOn time.Time
				for rows.Next() {
					rows.Scan(&id, &chatID, &messageID, &message, &fileID, &fileType, &enqueuedOn, &fireOn, &deliveredOn)

					queue = append(queue, QueueItem{
						ID:          id,
						ChatID:      chatID,
						MessageID:   messageID,
						Message:     message,
						FileID:      fileID,
						FileType:    fileType,
						EnqueuedOn:  enqueuedOn,
						FireOn:      fireOn,
						DeliveredOn: deliveredOn,
					})
				}
			}
		}

		defer tx.Commit()
	} else {
		log.Printf("failed to begin transaction: %s", err)
	}

	return queue
}

// UndeliveredQueueItems fetches all undelivered items from the queue.
func (d *OracleDatabase) UndeliveredQueueItems(chatID int64) []QueueItem {
	queue := []QueueItem{}

	if tx, err := d.db.Begin(); err == nil {
		if stmt, err := d.db.Prepare(fmt.Sprintf(`select 
			id,
			chat_id, 
			message_id,
			message, 
			file_id,
			file_type,
			enqueued_on,
			fire_on,
			delivered_on
			from %squeue
			where chat_id = :1 and delivered_on is null
			order by fire_on asc`, tablePrefix)); err != nil {
			log.Printf("* failed to prepare a statement: %s", err)
		} else {
			defer stmt.Close()

			if rows, err := stmt.Query(chatID); err != nil {
				log.Printf("* failed to select queue items from oracle database: %s", err)
			} else {
				defer rows.Close()

				var id, chatID int64
				var messageID int
				var message, fileID string
				var fileType FileType
				var enqueuedOn, fireOn, deliveredOn time.Time
				for rows.Next() {
					rows.Scan(&id, &chatID, &messageID, &message, &fileID, &fileType, &enqueuedOn, &fireOn, &deliveredOn)

					queue = append(queue, QueueItem{
						ID:          id,
						ChatID:      chatID,
						MessageID:   messageID,
						Message:     message,
						FileID:      fileID,
						FileType:    fileType,
						EnqueuedOn:  enqueuedOn,
						FireOn:      fireOn,
						DeliveredOn: deliveredOn,
					})
				}
			}
		}

		defer tx.Commit()
	} else {
		log.Printf("failed to begin transaction: %s", err)
	}

	return queue
}

// GetQueueItem fetches a queue item
func (d *OracleDatabase) GetQueueItem(chatID, queueID int64) (QueueItem, error) {
	var err error = nil

	if tx, err := d.db.Begin(); err == nil {
		var stmt *sql.Stmt

		if stmt, err = d.db.Prepare(fmt.Sprintf(`select 
			id,
			chat_id, 
			message_id,
			message, 
			file_id,
			file_type,
			enqueued_on,
			fire_on,
			delivered_on
			from %squeue
			where id = :1 and chat_id = :2`, tablePrefix)); err != nil {
			log.Printf("* failed to prepare a statement: %s", err)
		} else {
			defer stmt.Close()

			var rows *sql.Rows
			if rows, err = stmt.Query(queueID, chatID); err != nil {
				log.Printf("* failed to select a queue item from oracle database: %s", err)
			} else {
				defer rows.Close()

				var id, chatID int64
				var messageID int
				var message, fileID string
				var fileType FileType
				var enqueuedOn, fireOn, deliveredOn time.Time
				if rows.Next() {
					rows.Scan(&id, &chatID, &messageID, &message, &fileID, &fileType, &enqueuedOn, &fireOn, &deliveredOn)

					return QueueItem{
						ID:          id,
						ChatID:      chatID,
						MessageID:   messageID,
						Message:     message,
						FileID:      fileID,
						FileType:    fileType,
						EnqueuedOn:  enqueuedOn,
						FireOn:      fireOn,
						DeliveredOn: deliveredOn,
					}, nil
				}

				err = fmt.Errorf("no such queue item with id = %d, chat_id = %d", id, chatID)

				log.Printf("* failed to select a queue item with id = %d, chat_id = %d from oracle database", id, chatID)
			}
		}

		defer tx.Commit()
	} else {
		log.Printf("failed to begin transaction: %s", err)
	}

	return QueueItem{}, err
}

// DeleteQueueItem deletes a queue item
func (d *OracleDatabase) DeleteQueueItem(chatID, queueID int64) bool {
	result := false

	if tx, err := d.db.Begin(); err == nil {
		if stmt, err := d.db.Prepare(fmt.Sprintf(`delete from %squeue where id = :1 and chat_id = :2`, tablePrefix)); err != nil {
			log.Printf("* failed to prepare a statement: %s", err)
		} else {
			defer stmt.Close()

			if _, err = stmt.Exec(queueID, chatID); err != nil {
				log.Printf("* failed to delete queue item from oracle database: %s", err)
			} else {
				result = true
			}
		}

		defer tx.Commit()
	} else {
		log.Printf("failed to begin transaction: %s", err)

	}

	return result
}

// IncreaseNumTries increases the number of tries of a queue item
func (d *OracleDatabase) IncreaseNumTries(chatID, queueID int64) bool {
	result := false

	if tx, err := d.db.Begin(); err == nil {
		if stmt, err := d.db.Prepare(fmt.Sprintf(`update %squeue set num_tries = num_tries + 1 where id = :1 and chat_id = :2`, tablePrefix)); err != nil {
			log.Printf("* failed to prepare a statement: %s", err)
		} else {
			defer stmt.Close()

			var res sql.Result
			if res, err = stmt.Exec(queueID, chatID); err != nil {
				log.Printf("* failed to increase num_tries in oracle database: %s", err)
			} else {
				if num, _ := res.RowsAffected(); num <= 0 {
					log.Printf("* failed to increase num_tires for id: %d, chat_id: %d", queueID, chatID)
				} else {
					result = true
				}
			}
		}

		defer tx.Commit()
	} else {
		log.Printf("failed to begin transaction: %s", err)
	}

	return result
}

// MarkQueueItemAsDelivered makes a queue item as delivered
func (d *OracleDatabase) MarkQueueItemAsDelivered(chatID, queueID int64) bool {
	result := false

	if tx, err := d.db.Begin(); err == nil {
		if stmt, err := d.db.Prepare(fmt.Sprintf(`update %squeue set delivered_on = :1 where id = :2 and chat_id = :3`, tablePrefix)); err != nil {
			log.Printf("* failed to prepare a statement: %s", err)
		} else {
			defer stmt.Close()

			now := time.Now()

			var res sql.Result
			if res, err = stmt.Exec(now, queueID, chatID); err != nil {
				log.Printf("* failed to mark delivered_on in oracle database: %s", err)
			} else {
				if num, _ := res.RowsAffected(); num <= 0 {
					log.Printf("* failed to mark delivered_on for id: %d, chat_id: %d", queueID, chatID)
				} else {
					result = true
				}
			}
		}

		defer tx.Commit()
	} else {
		log.Printf("failed to begin transaction: %s", err)
	}

	return result
}
