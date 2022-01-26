package database

import (
	"fmt"
	"log"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// OpenSQLiteDB opens a connection to a sqlite file.
func OpenSQLiteDB(filepath string) (*Database, error) {
	if _database == nil {
		var db *gorm.DB
		var err error

		if db, err = gorm.Open(sqlite.Open(filepath), &gorm.Config{}); err != nil {
			return nil, fmt.Errorf("failed to open database: %s", err)
		}

		_database = &Database{
			db: db,
		}

		_database.doMigrations()
	}

	return _database, nil
}

// CloseSQLiteDB closes database
func (d *Database) CloseSQLiteDB() {
	if d.db != nil {
		if db, err := d.db.DB(); err == nil {
			db.Close()
		} else {
			log.Printf("* failed to close database: %s", err)
		}
	}

	_database = nil
}
