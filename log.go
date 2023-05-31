package main

import (
	"log"
	"os"

	"github.com/meinside/telegram-bot-reminder/database"
)

var _stdout = log.New(os.Stdout, "", log.LstdFlags)
var _stderr = log.New(os.Stderr, "", log.LstdFlags)

// log info message
func logInfo(format string, a ...any) {
	_stdout.Printf(format, a...)
}

// log debug message (printed to stdout only when `IsVerbose` is true)
func logDebug(conf config, format string, a ...any) {
	if conf.IsVerbose {
		_stdout.Printf(format, a...)
	}
}

// log error message
func logError(db *database.Database, format string, a ...any) {
	if db != nil {
		db.LogError(format, a...)
	}

	_stderr.Printf(format, a...)
}

// log error message and exit(1)
func logErrorAndDie(db *database.Database, format string, a ...any) {
	if db != nil {
		db.LogError(format, a...)
	}

	_stderr.Fatalf(format, a...)
}
