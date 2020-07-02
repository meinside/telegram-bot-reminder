package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	lkdp "github.com/meinside/lazy-korean-date-parser-go"
	bot "github.com/meinside/telegram-bot-go"
	"github.com/meinside/telegram-bot-reminder/database"
)

const (
	configFilename = "config.json"
	dbFilename     = "db.sqlite"

	commandStart                    = "/start"
	commandListReminders            = "/list"
	commandLoad                     = "/load"
	commandCancel                   = "/cancel"
	commandHelp                     = "/help"
	descriptionCommandListReminders = "알림 조회"
	descriptionCommandCancel        = "알림 취소"
	descriptionCommandHelp          = "도움말 보기"

	defaultDatetimeFormat = "2006.01.02 15:04" // yyyy.mm.dd hh:MM

	defaultHour = 8 // XXX - 08:00 as default

	messageCancel                 = "취소"
	messageCommandCanceled        = "명령이 취소 되었습니다."
	messageReminderCanceledFormat = "알림이 취소 되었습니다: %s"
	messageError                  = "오류가 발생했습니다."
	messageErrorFormat            = "오류가 발생했습니다: %s"
	messageNoReminders            = "예약된 알림이 없습니다."
	messageNoDateTime             = "유효한 날짜 또는 시간이 없습니다: %s"
	messageListItemFormat         = "☑ %s; %s"
	messageResponseFormat         = `%s에 "%s" 알림 예정입니다.`
	messageSaveFailedFormat       = "알림 저장을 실패 했습니다: %s (%s)"
	messageParseFailedFormat      = "메시지를 이해하지 못했습니다: %s"
	messageSelectWhat             = "어떤 시간을 원하십니까?"
	messageCancelWhat             = "어떤 알림을 취소하시겠습니까?"
	messageTimeIsPast             = "이미 지난 시각입니다."
	messageSendingBackFile        = "받은 파일을 즉시 다시 보내드립니다."
	messageWillSendBackFileFormat = "@%s님에게 받은 파일(%s)을 %s에 보내드리겠습니다."
	messageUsage                  = `사용법:

* 기본 사용 방법:
날짜 또는 시간이 포함된 메시지, 또는 caption이 포함된 파일을 보내면
인식한 해당 날짜/시간에 해당 메시지를 다시 보내줍니다.

* 사용 예:
"내일 이 메시지 다시 보내줄래?"
"18:30 알림"
"7시 반 출근"
"2016-12-31 오후 11시에 신년 타종행사 보라고 알려다오"
"1시간 뒤 가스 불 끄기"
"28일 후엔 좀비들이 다 굶어 죽었다더라"

* 기타 명령어:
/list : 예약된 알림 조회
/cancel : 예약된 알림 취소
/help : 본 사용법 확인

* 문의:
https://github.com/meinside/telegram-bot-reminder
`
)

var telegram *bot.Bot
var _location *time.Location

var _conf config
var _maxNumTries int
var _monitorIntervalSeconds int
var _telegramIntervalSeconds int
var _restrictUsers bool
var _allowedUserIds []string

var _isVerbose bool

type oracleDatabaseConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
	SID      string `json:"sid"`
}

type config struct {
	TelegramAPIToken        string                `json:"telegram_api_token"`
	MonitorIntervalSeconds  int                   `json:"monitor_interval_seconds"`
	TelegramIntervalSeconds int                   `json:"telegram_interval_seconds"`
	MaxNumTries             int                   `json:"max_num_tries"`
	RestrictUsers           bool                  `json:"restrict_users,omitempty"`
	AllowedUserIds          []string              `json:"allowed_user_ids"`
	OracleDatabaseConfig    *oracleDatabaseConfig `json:"oracle_db_config,omitempty"`
	IsVerbose               bool                  `json:"is_verbose,omitempty"`
}

var _stdout = log.New(os.Stdout, "", log.LstdFlags)
var _stderr = log.New(os.Stderr, "", log.LstdFlags)

// DatabaseInterface for interfacing databases (sqlite, oracle, ...)
type DatabaseInterface interface {
	Log(msg string)
	LogError(msg string)
	GetLogs(latestN int) ([]database.Log, error)
	SaveTemporaryMessage(chatID int64, messageID int, message, fileID string, fileType database.FileType) (bool, error)
	LoadTemporaryMessage(chatID int64, messageID int) (database.TemporaryMessage, error)
	DeleteTemporaryMessage(chatID int64, messageID int) (bool, error)
	Enqueue(chatID int64, messageID int, message, fileID string, fileType database.FileType, fireOn time.Time) (bool, error)
	DeliverableQueueItems(maxNumTries int) ([]database.QueueItem, error)
	UndeliveredQueueItems(chatID int64) ([]database.QueueItem, error)
	GetQueueItem(chatID, queueID int64) (database.QueueItem, error)
	DeleteQueueItem(chatID, queueID int64) (bool, error)
	IncreaseNumTries(chatID, queueID int64) (bool, error)
	MarkQueueItemAsDelivered(chatID, queueID int64) (bool, error)
}

var db DatabaseInterface

func pwd() string {
	var execPath string
	var err error
	if execPath, err = os.Executable(); err == nil {
		return filepath.Dir(execPath)
	}

	_stderr.Printf("failed to get executable path: %s", err)

	return "." // fallback to 'current directory'
}

func openConfig() (conf config, err error) {
	confFilepath := filepath.Join(pwd(), configFilename)

	_stdout.Printf("reading config: %s", confFilepath)

	var file []byte
	if file, err = ioutil.ReadFile(confFilepath); err == nil {
		if err = json.Unmarshal(file, &conf); err == nil {
			return conf, nil
		}
	}

	return config{}, err
}

func init() {
	var err error
	if _conf, err = openConfig(); err != nil {
		panic(err)
	} else {
		if _conf.MonitorIntervalSeconds <= 0 {
			_conf.MonitorIntervalSeconds = 10
		}
		_monitorIntervalSeconds = _conf.MonitorIntervalSeconds

		if _conf.TelegramIntervalSeconds <= 0 {
			_conf.TelegramIntervalSeconds = 1
		}
		_telegramIntervalSeconds = _conf.TelegramIntervalSeconds

		if _conf.MaxNumTries < 0 {
			_conf.MaxNumTries = 10
		}
		_maxNumTries = _conf.MaxNumTries

		_restrictUsers = _conf.RestrictUsers
		_allowedUserIds = _conf.AllowedUserIds

		telegram = bot.NewClient(_conf.TelegramAPIToken)
		telegram.Verbose = _conf.IsVerbose

		dbFilepath := filepath.Join(pwd(), dbFilename)

		if _conf.IsVerbose {
			_stdout.Printf("reading database: %s", dbFilepath)
		}

		var err error
		if _conf.OracleDatabaseConfig != nil {
			// open oracle database connection,
			if db, err = database.OpenOracleDB(_conf.OracleDatabaseConfig.Username, _conf.OracleDatabaseConfig.Password, _conf.OracleDatabaseConfig.SID); err != nil {
				panic(err)
			}
		} else {
			// fallback - load sqlite db,
			if db, err = database.OpenSQLiteDB(dbFilepath); err != nil {
				panic(err)
			}
		}

		_location, _ = time.LoadLocation("Local")
		_isVerbose = _conf.IsVerbose
	}
}

// check if given Telegram id is allowed or not
func isAllowedID(id string) bool {
	if _restrictUsers == false {
		return true
	}

	for _, v := range _allowedUserIds {
		if v == id {
			return true
		}
	}

	return false
}

func monitorQueue(monitor *time.Ticker, client *bot.Bot) {
	for {
		select {
		case <-monitor.C:
			processQueue(client)
		}
	}
}

func processQueue(client *bot.Bot) {
	if queue, err := db.DeliverableQueueItems(_maxNumTries); err == nil {
		if _isVerbose {
			_stdout.Printf("checking queue: %d items...", len(queue))
		}

		for _, q := range queue {
			go func(q database.QueueItem) {
				message := fmt.Sprintf("%s", q.Message)

				options := defaultOptions()
				options["reply_to_message_id"] = q.MessageID // show original message

				var sent bot.APIResponseMessage

				// if it is a message with a file,
				if q.FileID != "" && q.FileType != "" {
					switch q.FileType {
					case database.FileTypeDocument:
						options["caption"] = message
						sent = client.SendDocument(q.ChatID, bot.InputFileFromFileID(q.FileID), options)
					case database.FileTypeAudio:
						options["caption"] = message
						sent = client.SendAudio(q.ChatID, bot.InputFileFromFileID(q.FileID), options)
					case database.FileTypePhoto:
						options["caption"] = message
						sent = client.SendPhoto(q.ChatID, bot.InputFileFromFileID(q.FileID), options)
					case database.FileTypeSticker:
						sent = client.SendSticker(q.ChatID, bot.InputFileFromFileID(q.FileID), options)
					case database.FileTypeVideo:
						options["caption"] = message
						sent = client.SendVideo(q.ChatID, bot.InputFileFromFileID(q.FileID), options)
					case database.FileTypeVoice:
						sent = client.SendVoice(q.ChatID, bot.InputFileFromFileID(q.FileID), options)
					}
				} else {
					// if it is just a message,
					sent = client.SendMessage(q.ChatID, message, options)
				}

				if sent.Ok {
					// mark as delivered
					if _, err := db.MarkQueueItemAsDelivered(q.ChatID, q.ID); err != nil {
						_stderr.Printf("failed to mark chat id: %d, queue id: %d (%s)", q.ChatID, q.ID, err)
					}
				} else {
					_stderr.Printf("failed to send reminder: %s", *sent.Description)
				}

				// increase num tries
				if _, err := db.IncreaseNumTries(q.ChatID, q.ID); err != nil {
					_stderr.Printf("failed to increase num tries for chat id: %d, queue id: %d (%s)", q.ChatID, q.ID, err)
				}
			}(q)
		}
	} else {
		_stderr.Printf("failed to process queue: %s", err)
	}
}

func processUpdate(b *bot.Bot, update bot.Update, err error) {
	if err == nil {
		if update.HasMessage() {
			username := *update.Message.From.Username

			if !isAllowedID(username) {
				_stderr.Printf("id not allowed: %s", username)

				return
			}

			chatID := update.Message.Chat.ID

			// 'is typing...'
			b.SendChatAction(chatID, bot.ChatActionTyping)

			message := ""
			options := defaultOptions()

			if update.Message.HasText() { // text
				txt := *update.Message.Text

				if strings.HasPrefix(txt, commandStart) { // /start
					message = messageUsage
				} else if strings.HasPrefix(txt, commandListReminders) {
					if reminders, err := db.UndeliveredQueueItems(chatID); err == nil {
						if len(reminders) > 0 {
							format := fmt.Sprintf("%s\n", messageListItemFormat)
							for _, r := range reminders {
								message += fmt.Sprintf(format, r.FireOn.In(_location).Format(defaultDatetimeFormat), r.Message)
							}
						} else {
							message = messageNoReminders
						}
					} else {
						_stderr.Printf("failed to process %s: %s", commandListReminders, err)
					}
				} else if strings.HasPrefix(txt, commandCancel) {
					if reminders, err := db.UndeliveredQueueItems(chatID); err == nil {
						if len(reminders) > 0 {
							// inline keyboards
							keys := make(map[string]string)
							for _, r := range reminders {
								keys[fmt.Sprintf(messageListItemFormat, r.FireOn.In(_location).Format(defaultDatetimeFormat), r.Message)] = fmt.Sprintf("%s %d", commandCancel, r.ID)
							}
							buttons := bot.NewInlineKeyboardButtonsAsRowsWithCallbackData(keys)

							// add a cancel button for canceling reminder
							cancel := commandCancel
							buttons = append(buttons, []bot.InlineKeyboardButton{
								bot.InlineKeyboardButton{
									Text:         messageCancel,
									CallbackData: &cancel,
								},
							})

							// options
							options["reply_markup"] = bot.InlineKeyboardMarkup{
								InlineKeyboard: buttons,
							}

							message = messageCancelWhat
						} else {
							message = messageNoReminders
						}
					} else {
						_stderr.Printf("failed to process %s: %s", commandCancel, err)
					}
				} else if strings.HasPrefix(txt, commandHelp) {
					message = messageUsage
				} else {
					if whens, _, err := parseMessage(txt); err == nil {
						if len(whens) == 1 {
							when := whens[0]

							if _, err := db.Enqueue(chatID, update.Message.MessageID, txt, "", "", when); err == nil {
								message = fmt.Sprintf(messageResponseFormat,
									when.In(_location).Format(defaultDatetimeFormat),
									txt,
								)
							} else {
								message = fmt.Sprintf(messageSaveFailedFormat, txt, err)
							}
						} else {
							if _, err := db.SaveTemporaryMessage(chatID, update.Message.MessageID, txt, "", ""); err == nil {
								message = messageSelectWhat

								// options for inline keyboards
								options["reply_markup"] = bot.InlineKeyboardMarkup{
									InlineKeyboard: datetimeButtonsForCallbackQuery(whens, chatID, update.Message.MessageID),
								}
							} else {
								message = messageError
							}
						}
					} else {
						message = fmt.Sprintf(messageParseFailedFormat, err)
					}
				}
			} else {
				processOthers(b, update)
				return
			}

			// send message
			if len(message) <= 0 {
				message = messageError
			}
			if sent := b.SendMessage(chatID, message, options); !sent.Ok {
				_stderr.Printf("failed to send message: %s", *sent.Description)
			}
		} else if update.HasCallbackQuery() {
			processCallbackQuery(b, update)
		}
	} else {
		_stderr.Printf("error while receiving update (%s)", err.Error())
	}
}

// process incoming callback query
func processCallbackQuery(b *bot.Bot, update bot.Update) bool {
	// process result
	result := false

	query := *update.CallbackQuery
	data := *query.Data

	var message = messageError

	if strings.HasPrefix(data, commandCancel) {
		if data == commandCancel {
			message = messageCommandCanceled
		} else {
			cancelParam := strings.TrimSpace(strings.Replace(data, commandCancel, "", 1))
			if queueID, err := strconv.Atoi(cancelParam); err == nil {
				if item, err := db.GetQueueItem(query.Message.Chat.ID, int64(queueID)); err == nil {
					if _, err := db.DeleteQueueItem(query.Message.Chat.ID, int64(queueID)); err == nil {
						message = fmt.Sprintf(messageReminderCanceledFormat, item.Message)
					} else {
						_stderr.Printf("failed to delete reminder: %s", err)
					}
				} else {
					_stderr.Printf("failed to get reminder: %s", err)
				}
			} else {
				_stderr.Printf("unprocessable callback query: %s", data)
			}
		}
	} else if strings.HasPrefix(data, commandLoad) {
		params := strings.Split(strings.TrimSpace(strings.Replace(data, commandLoad, "", 1)), "/")

		if len(params) >= 3 {
			if chatID, err := strconv.ParseInt(params[0], 10, 64); err == nil {
				if messageID, err := strconv.Atoi(params[1]); err == nil {
					if saved, err := db.LoadTemporaryMessage(chatID, messageID); err == nil {
						if when, err := time.ParseInLocation(defaultDatetimeFormat, params[2], _location); err == nil {
							if _, err := db.Enqueue(chatID, messageID, saved.Message, saved.FileID, saved.FileType, when); err == nil {
								message = fmt.Sprintf(messageResponseFormat,
									when.In(_location).Format(defaultDatetimeFormat),
									saved.Message,
								)

								// delete temporary message
								if _, err := db.DeleteTemporaryMessage(chatID, messageID); err != nil {
									_stderr.Printf("failed to delete temporary message: %s", err)
								}
							} else {
								message = fmt.Sprintf(messageSaveFailedFormat, saved.Message, err)
							}
						} else {
							_stderr.Printf("failed to parse time: %s", err)
						}
					} else {
						_stderr.Printf("failed to load temporary message with chat id: %d, message id: %d", chatID, messageID)
					}
				} else {
					_stderr.Printf("failed to convert message id: %s", err)
				}
			} else {
				_stderr.Printf("failed to convert chat id: %s", err)
			}
		} else {
			_stderr.Printf("malformed inline keyboard data: %s", data)
		}
	} else {
		_stderr.Printf("unprocessable callback query: %s", data)
	}

	// answer callback query
	if apiResult := b.AnswerCallbackQuery(query.ID, map[string]interface{}{"text": message}); apiResult.Ok {
		// edit message and remove inline keyboards
		options := map[string]interface{}{
			"chat_id":    query.Message.Chat.ID,
			"message_id": query.Message.MessageID,
		}
		if apiResult := b.EditMessageText(message, options); apiResult.Ok {
			result = true
		} else {
			_stderr.Printf("failed to edit message text: %s", *apiResult.Description)

			db.LogError(fmt.Sprintf("failed to edit message text: %s", *apiResult.Description))
		}
	} else {
		_stderr.Printf("failed to answer callback query: %+v", query)

		db.LogError(fmt.Sprintf("failed to answer callback query: %+v", query))
	}

	return result
}

func processOthers(b *bot.Bot, update bot.Update) bool {
	success := false

	var message string
	chatID := update.Message.Chat.ID
	username := *update.Message.From.Username
	options := defaultOptions()

	if update.Message.HasDocument() { // file
		fileID := update.Message.Document.FileID

		if update.Message.HasCaption() {
			txt := *update.Message.Caption

			if whens, _, err := parseMessage(txt); err == nil {
				if len(whens) == 1 {
					when := whens[0]

					// enqueue received file
					if _, err := db.Enqueue(chatID, update.Message.MessageID, txt, fileID, database.FileTypeDocument, when); err == nil {
						message = fmt.Sprintf(messageWillSendBackFileFormat,
							username,
							"file",
							when.In(_location).Format(defaultDatetimeFormat),
						)

						success = true
					} else {
						message = fmt.Sprintf(messageSaveFailedFormat, txt, err)
					}
				} else {
					if _, err := db.SaveTemporaryMessage(chatID, update.Message.MessageID, txt, fileID, database.FileTypeDocument); err == nil {
						message = messageSelectWhat

						// options for inline keyboards
						options["reply_markup"] = bot.InlineKeyboardMarkup{
							InlineKeyboard: datetimeButtonsForCallbackQuery(whens, chatID, update.Message.MessageID),
						}

						success = true
					} else {
						message = messageError
					}
				}
			} else {
				message = fmt.Sprintf(messageParseFailedFormat, err)
			}

			if sent := b.SendMessage(chatID, message, options); !sent.Ok {
				_stderr.Printf("failed to send message: %s", *sent.Description)
			}
		} else {
			// send received file back immediately
			options["caption"] = messageSendingBackFile
			if sent := b.SendDocument(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
				success = true
			} else {
				_stderr.Printf("failed to send document back: %s", *sent.Description)
			}
		}
	} else if update.Message.HasAudio() { // audio
		fileID := update.Message.Audio.FileID

		if update.Message.HasCaption() {
			txt := *update.Message.Caption

			if whens, _, err := parseMessage(txt); err == nil {
				if len(whens) == 1 {
					when := whens[0]

					// enqueue received file
					if _, err := db.Enqueue(chatID, update.Message.MessageID, txt, fileID, database.FileTypeAudio, when); err == nil {
						message = fmt.Sprintf(messageWillSendBackFileFormat,
							username,
							"audio",
							when.In(_location).Format(defaultDatetimeFormat),
						)

						success = true
					} else {
						message = fmt.Sprintf(messageSaveFailedFormat, txt, err)
					}
				} else {
					if _, err := db.SaveTemporaryMessage(chatID, update.Message.MessageID, txt, fileID, database.FileTypeAudio); err == nil {
						message = messageSelectWhat

						// options for inline keyboards
						options["reply_markup"] = bot.InlineKeyboardMarkup{
							InlineKeyboard: datetimeButtonsForCallbackQuery(whens, chatID, update.Message.MessageID),
						}

						success = true
					} else {
						message = messageError
					}
				}
			} else {
				message = fmt.Sprintf(messageParseFailedFormat, err)
			}

			if sent := b.SendMessage(chatID, message, options); !sent.Ok {
				_stderr.Printf("failed to send message: %s", *sent.Description)
			}
		} else {
			// send received file back immediately
			options["caption"] = messageSendingBackFile
			if sent := b.SendAudio(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
				success = true
			} else {
				_stderr.Printf("failed to send audio back: %s", *sent.Description)
			}
		}
	} else if update.Message.HasPhoto() { // photo
		photo := update.Message.LargestPhoto()
		fileID := photo.FileID

		if update.Message.HasCaption() {
			txt := *update.Message.Caption

			if whens, _, err := parseMessage(txt); err == nil {
				if len(whens) == 1 {
					when := whens[0]

					// enqueue received file
					if _, err := db.Enqueue(chatID, update.Message.MessageID, txt, fileID, database.FileTypePhoto, when); err == nil {
						message = fmt.Sprintf(messageWillSendBackFileFormat,
							username,
							"image",
							when.In(_location).Format(defaultDatetimeFormat),
						)

						success = true
					} else {
						message = fmt.Sprintf(messageSaveFailedFormat, txt, err)
					}
				} else {
					if _, err := db.SaveTemporaryMessage(chatID, update.Message.MessageID, txt, fileID, database.FileTypePhoto); err == nil {
						message = messageSelectWhat

						// options for inline keyboards
						options["reply_markup"] = bot.InlineKeyboardMarkup{
							InlineKeyboard: datetimeButtonsForCallbackQuery(whens, chatID, update.Message.MessageID),
						}

						success = true
					} else {
						message = messageError
					}
				}
			} else {
				message = fmt.Sprintf(messageParseFailedFormat, err)
			}

			if sent := b.SendMessage(chatID, message, options); !sent.Ok {
				_stderr.Printf("failed to send message: %s", *sent.Description)
			}
		} else {
			options["caption"] = messageSendingBackFile

			// send received file back immediately
			if sent := b.SendPhoto(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
				success = true
			} else {
				_stderr.Printf("failed to send photo back: %s", *sent.Description)
			}
		}
	} else if update.Message.HasSticker() { // sticker (has no caption)
		fileID := update.Message.Sticker.FileID

		// send received file back immediately
		if sent := b.SendSticker(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
			success = true
		} else {
			_stderr.Printf("failed to send sticker back: %s", *sent.Description)
		}
	} else if update.Message.HasVideo() { // video
		fileID := update.Message.Video.FileID

		if update.Message.HasCaption() {
			txt := *update.Message.Caption

			if whens, _, err := parseMessage(txt); err == nil {
				if len(whens) == 1 {
					when := whens[0]

					// enqueue received file
					if _, err := db.Enqueue(chatID, update.Message.MessageID, txt, fileID, database.FileTypeVideo, when); err == nil {
						message = fmt.Sprintf(messageWillSendBackFileFormat,
							username,
							"image",
							when.In(_location).Format(defaultDatetimeFormat),
						)

						success = true
					} else {
						message = fmt.Sprintf(messageSaveFailedFormat, txt, err)
					}
				} else {
					if _, err := db.SaveTemporaryMessage(chatID, update.Message.MessageID, txt, fileID, database.FileTypeVideo); err == nil {
						message = messageSelectWhat

						// options for inline keyboards
						options["reply_markup"] = bot.InlineKeyboardMarkup{
							InlineKeyboard: datetimeButtonsForCallbackQuery(whens, chatID, update.Message.MessageID),
						}

						success = true
					} else {
						message = messageError
					}
				}
			} else {
				message = fmt.Sprintf(messageParseFailedFormat, err)
			}

			if sent := b.SendMessage(chatID, message, options); !sent.Ok {
				_stderr.Printf("failed to send message: %s", *sent.Description)
			}
		} else {
			// send received file back immediately
			options["caption"] = messageSendingBackFile
			if sent := b.SendVideo(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
				success = true
			} else {
				_stderr.Printf("failed to send video back: %s", *sent.Description)
			}
		}
	} else if update.Message.HasVoice() { // voice (has no caption)
		fileID := update.Message.Voice.FileID

		// send received file back immediately
		options["caption"] = messageSendingBackFile
		if sent := b.SendVoice(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
			success = true
		} else {
			_stderr.Printf("failed to send voice back: %s", *sent.Description)
		}
	}

	return success
}

func parseMessage(message string) (candidates []time.Time, what string, err error) {
	now := time.Now()
	what = fmt.Sprintf("%s", message) // XXX - edit this?

	dates := map[string]time.Time{}
	times := map[string]lkdp.Hms{}
	if dates, err = lkdp.ExtractDates(message, true); err == nil {
		if times, err = lkdp.ExtractTimes(message, false); err != nil {
			times = map[string]lkdp.Hms{
				"default": {Hours: defaultHour, Minutes: 0},
			}
		}

		// generate combinations from parsed dates and times
		for _, d := range dates {
			for _, t := range times {
				d = d.Add(time.Duration(t.Hours) * time.Hour).Add(time.Duration(t.Minutes) * time.Minute)

				candidates = append(candidates, d)

				// if the candidate is in AM, also add PM version of it
				if t.Hours < 12 && t.Hours > 0 && t.Ambiguous {
					d = d.Add(time.Hour * 12)

					candidates = append(candidates, d)
				}
			}
		}
	} else {
		var when time.Time
		if times, err = lkdp.ExtractTimes(message, false); err == nil {
			for _, t := range times {
				when = time.Date(now.Year(), now.Month(), now.Day(), t.Hours, t.Minutes, 0, 0, _location)

				candidates = append(candidates, when)

				// if the candidate is in AM, also add PM version of it
				if t.Hours < 12 && t.Hours > 0 && t.Ambiguous {
					when = when.Add(time.Hour * 12)

					candidates = append(candidates, when)
				}
			}
		} else {
			return nil, "", fmt.Errorf(messageNoDateTime, message)
		}
	}

	// remove duplicates from candidates
	candidates = uniqueTimes(candidates)

	// remove past datetimes from candidates
	var filteredPast bool
	candidates, filteredPast = filterPastTimes(now, candidates)

	// sort candidates by datetime (asc)
	sortTimes(candidates)

	// check if candidates is empty
	if len(candidates) > 0 {
		return candidates, message, nil
	}

	if filteredPast {
		err = fmt.Errorf(messageTimeIsPast)
	} else {
		err = fmt.Errorf(messageNoDateTime, message)
	}

	return nil, "", err
}

// filter unique times
func uniqueTimes(times []time.Time) []time.Time {
	m := map[time.Time]bool{}
	uniques := []time.Time{}

	for _, t := range times {
		if _, exists := m[t]; !exists {
			m[t] = true
			uniques = append(uniques, t)
		}
	}

	return uniques
}

// filter out past times
func filterPastTimes(when time.Time, times []time.Time) ([]time.Time, bool) {
	pastTimesExist := false
	filtered := []time.Time{}

	for _, t := range times {
		if t.Unix() > when.Unix() {
			filtered = append(filtered, t)
		} else {
			pastTimesExist = true
		}
	}

	return filtered, pastTimesExist
}

// sort given times
func sortTimes(times []time.Time) {
	sort.Slice(times, func(i, j int) bool {
		return times[i].Before(times[j])
	})
}

// generate inline keyboard buttons for multiple datetimes
func datetimeButtonsForCallbackQuery(times []time.Time, chatID int64, messageID int) [][]bot.InlineKeyboardButton {
	// datetime buttons
	keys := make(map[string]string)
	for _, w := range times {
		keys[w.In(_location).Format(defaultDatetimeFormat)] = fmt.Sprintf("%s %d/%d/%s", commandLoad, chatID, messageID, w.In(_location).Format(defaultDatetimeFormat))
	}
	buttons := bot.NewInlineKeyboardButtonsAsRowsWithCallbackData(keys)

	// add cancel button
	cancel := commandCancel
	buttons = append(buttons, []bot.InlineKeyboardButton{
		bot.InlineKeyboardButton{
			Text:         messageCancel,
			CallbackData: &cancel,
		},
	})

	return buttons
}

// default message options
func defaultOptions() map[string]interface{} {
	return map[string]interface{}{
		"reply_markup": bot.ReplyKeyboardMarkup{ // show keyboards
			Keyboard: [][]bot.KeyboardButton{
				bot.NewKeyboardButtons(commandListReminders, commandCancel, commandHelp),
			},
			ResizeKeyboard: true,
		},
	}
}

func main() {
	// monitor queue
	_stdout.Printf("starting monitoring queue...")
	go monitorQueue(
		time.NewTicker(time.Duration(_monitorIntervalSeconds)*time.Second),
		telegram,
	)

	// get info about this bot
	if me := telegram.GetMe(); me.Ok {
		if setCommands := telegram.SetMyCommands([]bot.BotCommand{
			bot.BotCommand{Command: commandListReminders, Description: descriptionCommandListReminders},
			bot.BotCommand{Command: commandCancel, Description: descriptionCommandCancel},
			bot.BotCommand{Command: commandHelp, Description: descriptionCommandHelp},
		}); !setCommands.Ok {
			_stderr.Printf("failed to set bot commands")
		}

		_stdout.Printf("starting bot: @%s (%s)", *me.Result.Username, me.Result.FirstName)

		// delete webhook (getting updates will not work when wehbook is set up)
		if unhooked := telegram.DeleteWebhook(); unhooked.Ok {
			// wait for new updates
			telegram.StartMonitoringUpdates(0, _telegramIntervalSeconds, processUpdate)
		} else {
			_stderr.Fatalf("failed to delete webhook")
		}
	} else {
		_stderr.Fatalf("failed to get info of the bot")
	}
}
