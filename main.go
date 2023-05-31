package main

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/meinside/telegram-bot-reminder/database"
)

const (
	configFilename = "config.json"
	dbFilename     = "db.sqlite"
)

type config struct {
	TelegramAPIToken        string   `json:"telegram_api_token"`
	MonitorIntervalSeconds  int      `json:"monitor_interval_seconds"`
	TelegramIntervalSeconds int      `json:"telegram_interval_seconds"`
	MaxNumTries             int      `json:"max_num_tries"`
	RestrictUsers           bool     `json:"restrict_users,omitempty"`
	AllowedUserIDs          []string `json:"allowed_user_ids"`
	IsVerbose               bool     `json:"is_verbose,omitempty"`
}

func pwd() string {
	var execPath string
	var err error
	if execPath, err = os.Executable(); err == nil {
		return filepath.Dir(execPath)
	}

	return "." // fallback to 'current directory'
}

func openConfig() (conf config, err error) {
	confFilepath := filepath.Join(pwd(), configFilename)

	var file []byte
	if file, err = ioutil.ReadFile(confFilepath); err == nil {
		if err = json.Unmarshal(file, &conf); err == nil {
			return conf, nil
		}
	}

	return config{}, err
}

func main() {
	if conf, err := openConfig(); err == nil {
		dbFilepath := filepath.Join(pwd(), dbFilename)

		logDebug(conf, "reading database: %s", dbFilepath)

		if db, err := database.OpenSQLiteDB(dbFilepath); err == nil {
			runBot(conf, db)
		} else {
			logErrorAndDie(nil, "failed to open database: %s", err)
		}
	} else {
		logErrorAndDie(nil, "failed to read config file: %s", err)
	}
}
