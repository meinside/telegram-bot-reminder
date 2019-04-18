package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	lkdp "github.com/meinside/lazy-korean-date-parser-go"
	bot "github.com/meinside/telegram-bot-go"

	"github.com/meinside/telegram-bot-reminder/helper"
)

const (
	configFilename = "config.json"
	dbFilename     = "db.sqlite"

	commandStart         = "/start"
	commandListReminders = "/list"
	commandCancel        = "/cancel"
	commandHelp          = "/help"

	defaultDatetimeFormat = "2006.01.02 15:04" // yyyy.mm.dd hh:MM

	messageCancel                 = "취소"
	messageCommandCanceled        = "명령이 취소 되었습니다."
	messageReminderCanceledFormat = "알림이 취소 되었습니다: %s"
	messageError                  = "오류가 발생했습니다."
	messageNoReminders            = "예약된 알림이 없습니다."
	messageNoDateTime             = "날짜 또는 시간이 없습니다."
	messageListItemFormat         = "☑ %s; %s"
	messageResponseFormat         = `@%s님에게 %s에 "%s" 알림 예정입니다.`
	messageSaveFailedFormat       = "알림 저장을 실패 했습니다: %s"
	messageParseFailedFormat      = "메시지를 이해하지 못했습니다: %s"
	messageCancelWhat             = "어떤 알림을 취소하시겠습니까?"
	messageTimeIsPastFormat       = "2006.01.02 15:04는 이미 지난 시각입니다"
	messageSendingBackFile        = "받은 파일을 즉시 다시 보내드립니다."
	messageWillSendBackFileFormat = "@%s님에게 받은 파일(%s)을 %s에 보내드리겠습니다."
	messageUsage                  = `사용법:

* 기본 사용 방법:
날짜 또는 시간이 포함된 메시지를 보내면,
인식한 해당 날짜/시간에 메시지를 다시 보내줍니다.

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
var db *helper.Database
var _location *time.Location

var _conf config
var _maxNumTries int
var _monitorIntervalSeconds int
var _telegramIntervalSeconds int
var _restrictUsers bool
var _allowedUserIds []string

var _isVerbose bool

type config struct {
	TelegramAPIToken        string   `json:"telegram_api_token"`
	MonitorIntervalSeconds  int      `json:"monitor_interval_seconds"`
	TelegramIntervalSeconds int      `json:"telegram_interval_seconds"`
	MaxNumTries             int      `json:"max_num_tries"`
	RestrictUsers           bool     `json:"restrict_users,omitempty"`
	AllowedUserIds          []string `json:"allowed_user_ids"`
	IsVerbose               bool     `json:"is_verbose,omitempty"`
}

func pwd() string {
	if execPath, err := os.Executable(); err == nil {
		return filepath.Dir(execPath)
	} else {
		log.Printf("failed to get executable path: %s", err)
	}

	return "." // fallback to 'current directory'
}

func openConfig() (conf config, err error) {
	var file []byte
	if file, err = ioutil.ReadFile(filepath.Join(pwd(), configFilename)); err == nil {
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

		db = helper.OpenDb(filepath.Join(pwd(), dbFilename))

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
	queue := db.DeliverableQueueItems(_maxNumTries)

	if _isVerbose {
		log.Printf("Checking queue: %d items...", len(queue))
	}

	for _, q := range queue {
		go func(q helper.QueueItem) {
			message := fmt.Sprintf("%s", q.Message)

			options := defaultOptions()
			options["reply_to_message_id"] = q.MessageID // show original message

			var sent bot.APIResponseMessage

			// if it is a message with a file,
			if q.FileID != "" && q.FileType != "" {
				switch q.FileType {
				case helper.FileTypeDocument:
					options["caption"] = message
					sent = client.SendDocument(q.ChatID, bot.InputFileFromFileID(q.FileID), options)
				case helper.FileTypeAudio:
					options["caption"] = message
					sent = client.SendAudio(q.ChatID, bot.InputFileFromFileID(q.FileID), options)
				case helper.FileTypePhoto:
					options["caption"] = message
					sent = client.SendPhoto(q.ChatID, bot.InputFileFromFileID(q.FileID), options)
				case helper.FileTypeSticker:
					sent = client.SendSticker(q.ChatID, bot.InputFileFromFileID(q.FileID), options)
				case helper.FileTypeVideo:
					options["caption"] = message
					sent = client.SendVideo(q.ChatID, bot.InputFileFromFileID(q.FileID), options)
				case helper.FileTypeVoice:
					sent = client.SendVoice(q.ChatID, bot.InputFileFromFileID(q.FileID), options)
				}
			} else {
				// if it is just a message,
				sent = client.SendMessage(q.ChatID, message, options)
			}

			if sent.Ok {
				// mark as delivered
				if !db.MarkQueueItemAsDelivered(q.ChatID, q.ID) {
					log.Printf("*** failed to mark chat id: %d, queue id: %d", q.ChatID, q.ID)
				}
			} else {
				log.Printf("*** failed to send reminder: %s", *sent.Description)
			}

			// increase num tries
			if !db.IncreaseNumTries(q.ChatID, q.ID) {
				log.Printf("*** failed to increase num tries for chat id: %d, queue id: %d", q.ChatID, q.ID)
			}
		}(q)
	}
}

func processUpdate(b *bot.Bot, update bot.Update, err error) {
	if err == nil {
		if update.HasMessage() {
			username := *update.Message.From.Username

			if !isAllowedID(username) {
				log.Printf("*** Id not allowed: %s", username)

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
					reminders := db.UndeliveredQueueItems(chatID)
					if len(reminders) > 0 {
						format := fmt.Sprintf("%s\n", messageListItemFormat)
						for _, r := range reminders {
							message += fmt.Sprintf(format, r.FireOn.Format(defaultDatetimeFormat), r.Message)
						}
					} else {
						message = messageNoReminders
					}
				} else if strings.HasPrefix(txt, commandCancel) {
					reminders := db.UndeliveredQueueItems(chatID)
					if len(reminders) > 0 {
						// inline keyboards
						keys := make(map[string]string)
						for _, r := range reminders {
							keys[fmt.Sprintf(messageListItemFormat, r.FireOn.Format(defaultDatetimeFormat), r.Message)] = fmt.Sprintf("%s %d", commandCancel, r.ID)
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
				} else if strings.HasPrefix(txt, commandHelp) {
					message = messageUsage
				} else {
					if when, what, err := parseMessage(txt); err == nil {
						if db.Enqueue(chatID, update.Message.MessageID, txt, "", "", when) {
							message = fmt.Sprintf(messageResponseFormat,
								username,
								when.Format(defaultDatetimeFormat),
								what,
							)
						} else {
							message = fmt.Sprintf(messageSaveFailedFormat, txt)
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
				log.Printf("*** failed to send message: %s", *sent.Description)
			}
		} else if update.HasCallbackQuery() {
			processCallbackQuery(b, update)
		}
	} else {
		log.Printf("*** error while receiving update (%s)", err.Error())
	}
}

// process incoming callback query
func processCallbackQuery(b *bot.Bot, update bot.Update) bool {
	// process result
	result := false

	query := *update.CallbackQuery
	txt := *query.Data

	var message = messageError
	if strings.HasPrefix(txt, commandCancel) {
		if txt == commandCancel {
			message = messageCommandCanceled
		} else {
			cancelParam := strings.TrimSpace(strings.Replace(txt, commandCancel, "", 1))
			if queueID, err := strconv.Atoi(cancelParam); err == nil {
				if item, err := db.GetQueueItem(query.Message.Chat.ID, int64(queueID)); err == nil {
					if db.DeleteQueueItem(query.Message.Chat.ID, int64(queueID)) {
						message = fmt.Sprintf(messageReminderCanceledFormat, item.Message)
					} else {
						log.Printf("*** Failed to delete reminder")
					}
				} else {
					log.Printf("*** Failed to get reminder: %s", err)
				}
			} else {
				log.Printf("*** Unprocessable callback query: %s", txt)
			}
		}
	} else {
		log.Printf("*** Unprocessable callback query: %s", txt)
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
			log.Printf("*** Failed to edit message text: %s", *apiResult.Description)

			db.LogError(fmt.Sprintf("failed to edit message text: %s", *apiResult.Description))
		}
	} else {
		log.Printf("*** Failed to answer callback query: %+v", query)

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
			if when, _, err := parseMessage(txt); err == nil {
				// enqueue received file
				if db.Enqueue(chatID, update.Message.MessageID, txt, fileID, helper.FileTypeDocument, when) {
					message = fmt.Sprintf(messageWillSendBackFileFormat,
						username,
						"file",
						when.Format(defaultDatetimeFormat),
					)
				} else {
					message = fmt.Sprintf(messageSaveFailedFormat, txt)
				}
			} else {
				message = fmt.Sprintf(messageParseFailedFormat, err)
			}

			if sent := b.SendMessage(chatID, message, options); !sent.Ok {
				log.Printf("*** failed to send message: %s", *sent.Description)
			}
		} else {
			// send received file back immediately
			options["caption"] = messageSendingBackFile
			if sent := b.SendDocument(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
				success = true
			} else {
				log.Printf("*** failed to send document back: %s", *sent.Description)
			}
		}
	} else if update.Message.HasAudio() { // audio
		fileID := update.Message.Audio.FileID

		if update.Message.HasCaption() {
			txt := *update.Message.Caption
			if when, _, err := parseMessage(txt); err == nil {
				// enqueue received file
				if db.Enqueue(chatID, update.Message.MessageID, txt, fileID, helper.FileTypeAudio, when) {
					message = fmt.Sprintf(messageWillSendBackFileFormat,
						username,
						"audio",
						when.Format(defaultDatetimeFormat),
					)

					success = true
				} else {
					message = fmt.Sprintf(messageSaveFailedFormat, txt)
				}
			} else {
				message = fmt.Sprintf(messageParseFailedFormat, err)
			}

			if sent := b.SendMessage(chatID, message, options); !sent.Ok {
				log.Printf("*** failed to send message: %s", *sent.Description)
			}
		} else {
			// send received file back immediately
			options["caption"] = messageSendingBackFile
			if sent := b.SendAudio(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
				success = true
			} else {
				log.Printf("*** failed to send audio back: %s", *sent.Description)
			}
		}
	} else if update.Message.HasPhoto() { // photo
		if update.Message.HasCaption() {
			txt := *update.Message.Caption
			if when, _, err := parseMessage(txt); err == nil {
				photo := update.Message.LargestPhoto()
				fileID := photo.FileID

				// enqueue received file
				if db.Enqueue(chatID, update.Message.MessageID, txt, fileID, helper.FileTypePhoto, when) {
					message = fmt.Sprintf(messageWillSendBackFileFormat,
						username,
						"image",
						when.Format(defaultDatetimeFormat),
					)

					success = true
				} else {
					message = fmt.Sprintf(messageSaveFailedFormat, txt)
				}
			} else {
				message = fmt.Sprintf(messageParseFailedFormat, err)
			}

			if sent := b.SendMessage(chatID, message, options); !sent.Ok {
				log.Printf("*** failed to send message: %s", *sent.Description)
			}
		} else {
			options["caption"] = messageSendingBackFile

			photo := update.Message.LargestPhoto()
			fileID := photo.FileID

			// send received file back immediately
			if sent := b.SendPhoto(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
				success = true
			} else {
				log.Printf("*** failed to send photo back: %s", *sent.Description)
			}
		}
	} else if update.Message.HasSticker() { // sticker (has no caption)
		fileID := update.Message.Sticker.FileID

		// send received file back immediately
		if sent := b.SendSticker(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
			success = true
		} else {
			log.Printf("*** failed to send sticker back: %s", *sent.Description)
		}
	} else if update.Message.HasVideo() { // video
		fileID := update.Message.Video.FileID

		if update.Message.HasCaption() {
			txt := *update.Message.Caption
			if when, _, err := parseMessage(txt); err == nil {
				// enqueue received file
				if db.Enqueue(chatID, update.Message.MessageID, txt, fileID, helper.FileTypeVideo, when) {
					message = fmt.Sprintf(messageWillSendBackFileFormat,
						username,
						"video",
						when.Format(defaultDatetimeFormat),
					)

					success = true
				} else {
					message = fmt.Sprintf(messageSaveFailedFormat, txt)
				}
			} else {
				message = fmt.Sprintf(messageParseFailedFormat, err)
			}

			if sent := b.SendMessage(chatID, message, options); !sent.Ok {
				log.Printf("*** failed to send message: %s", *sent.Description)
			}
		} else {
			// send received file back immediately
			options["caption"] = messageSendingBackFile
			if sent := b.SendVideo(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
				success = true
			} else {
				log.Printf("*** failed to send video back: %s", *sent.Description)
			}

		}
	} else if update.Message.HasVoice() { // voice (has no caption)
		fileID := update.Message.Voice.FileID

		// send received file back immediately
		options["caption"] = messageSendingBackFile
		if sent := b.SendVoice(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
			success = true
		} else {
			log.Printf("*** failed to send voice back: %s", *sent.Description)
		}
	}

	return success
}

func parseMessage(message string) (when time.Time, what string, err error) {
	now := time.Now()

	what = fmt.Sprintf("%s", message) // XXX - edit this?

	var hms lkdp.Hms
	if when, err = lkdp.ExtractDate(message, true); err == nil {
		if hms, err = lkdp.ExtractTime(message, false); err != nil {
			hms.Hours, hms.Minutes = 8, 0 // XXX - 08:00 as default
		}
		when = when.Add(time.Duration(hms.Hours) * time.Hour).Add(time.Duration(hms.Minutes) * time.Minute)
	} else {
		var daysChanged int
		if hms, err = lkdp.ExtractTime(message, false); err == nil {
			when = time.Date(now.Year(), now.Month(), now.Day(), hms.Hours, hms.Minutes, 0, 0, _location)
			if daysChanged != 0 {
				when = when.Add(time.Duration(daysChanged*24) * time.Hour)
			}
		} else {
			return time.Time{}, "", fmt.Errorf(messageNoDateTime)
		}
	}

	if when.Unix() >= now.Unix() {
		return when, what, nil
	}

	return time.Time{}, "", fmt.Errorf(when.Format(messageTimeIsPastFormat))
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
	log.Printf("> Starting monitoring queue...")
	go monitorQueue(
		time.NewTicker(time.Duration(_monitorIntervalSeconds)*time.Second),
		telegram,
	)

	// get info about this bot
	if me := telegram.GetMe(); me.Ok {
		log.Printf("> Starting bot: @%s (%s)", *me.Result.Username, me.Result.FirstName)

		// delete webhook (getting updates will not work when wehbook is set up)
		if unhooked := telegram.DeleteWebhook(); unhooked.Ok {
			// wait for new updates
			telegram.StartMonitoringUpdates(0, _telegramIntervalSeconds, processUpdate)
		} else {
			panic("failed to delete webhook")
		}
	} else {
		panic("failed to get info of the bot")
	}
}
