package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"strconv"
	"strings"
	"time"

	bot "github.com/meinside/telegram-bot-go"

	"github.com/meinside/lazy-korean-date-parser-go"
	"github.com/meinside/telegram-bot-reminder/helper"
)

const (
	DbFilename     = "db.sqlite"
	ConfigFilename = "config.json"

	CommandStart         = "/start"
	CommandListReminders = "/list"
	CommandCancel        = "/cancel"
	CommandHelp          = "/help"

	MessageCancel                 = "취소"
	MessageCommandCanceled        = "명령이 취소 되었습니다."
	MessageReminderCanceledFormat = "알림 %d: 취소 되었습니다."
	MessageError                  = "오류가 발생했습니다."
	MessageNoReminders            = "예약된 알림이 없습니다."
	MessageNoDateTime             = "날짜 또는 시간이 없습니다."
	MessageResponseFormat         = `@%s님에게 %s에 "%s" 알림 예정입니다.`
	MessageSaveFailedFormat       = "알림 저장을 실패 했습니다: %s"
	MessageParseFailedFormat      = "메시지를 이해하지 못했습니다: %s"
	MessageCancelWhat             = "어떤 알림을 취소하시겠습니까?"
	MessageUsage                  = `사용법:

* 기본 사용 방법:
날짜 또는 시간이 포함된 메시지를 보내면,
인식한 해당 날짜/시간에 메시지를 다시 보내줍니다.

* 예:
"내일 이 메시지 다시 보내줄래?"
"18:30 알림"
"2016-12-31 오후 11시에 신년 타종행사 보라고 알려다오"

* 기타 명령어:
/list : 예약된 알림 조회
/cancel : 예약된 알림 취소
/help : 본 사용법 확인
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
	TelegramApiToken        string   `json:"telegram_api_token"`
	MonitorIntervalSeconds  int      `json:"monitor_interval_seconds"`
	TelegramIntervalSeconds int      `json:"telegram_interval_seconds"`
	MaxNumTries             int      `json:"max_num_tries"`
	RestrictUsers           bool     `json:"restrict_users,omitempty"`
	AllowedUserIds          []string `json:"allowed_user_ids"`
	IsVerbose               bool     `json:"is_verbose,omitempty"`
}

func openConfig() (conf config, err error) {
	if file, err := ioutil.ReadFile(ConfigFilename); err == nil {
		if err := json.Unmarshal(file, &conf); err == nil {
			return conf, nil
		} else {
			return config{}, err
		}
	} else {
		return config{}, err
	}
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

		telegram = bot.NewClient(_conf.TelegramApiToken)
		db = helper.OpenDb(DbFilename)
		_location, _ = time.LoadLocation("Local")

		telegram.Verbose = _conf.IsVerbose
		_isVerbose = _conf.IsVerbose
	}
}

// check if given Telegram id is allowed or not
func isAllowedId(id string) bool {
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

func processQueue(client *bot.Bot) {
	queue := db.DeliverableQueueItems(_maxNumTries)

	if _isVerbose {
		log.Printf("Checking queue: %d items...\n", len(queue))
	}

	for _, q := range queue {
		go func(q helper.QueueItem) {
			// send message
			message := fmt.Sprintf("%s", q.Message)
			options := map[string]interface{}{
				"reply_to_message_id": q.MessageId, // show original message
			}
			if sent := client.SendMessage(q.ChatId, &message, options); !sent.Ok {
				log.Printf("*** failed to send reminder: %s\n", *sent.Description)
			} else {
				// mark as delivered
				if !db.MarkQueueItemAsDelivered(q.ChatId, q.Id) {
					log.Printf("*** failed to mark chat id: %d, queue id: %d\n", q.ChatId, q.Id)
				}
			}

			// increase num tries
			if !db.IncreaseNumTries(q.ChatId, q.Id) {
				log.Printf("*** failed to increase num tries for chat id: %d, queue id: %d\n", q.ChatId, q.Id)
			}
		}(q)
	}
}

func processUpdate(b *bot.Bot, update bot.Update, err error) {
	if err == nil {
		username := *update.Message.From.Username

		if !isAllowedId(username) {
			log.Printf("*** Id not allowed: %s\n", username)

			return
		}

		if update.HasMessage() {
			chatId := update.Message.Chat.Id

			// 'is typing...'
			b.SendChatAction(chatId, bot.ChatActionTyping)

			message := ""
			options := map[string]interface{}{
				"reply_markup": bot.ReplyKeyboardMarkup{ // show keyboards
					Keyboard: [][]bot.KeyboardButton{
						[]bot.KeyboardButton{
							bot.KeyboardButton{
								Text: CommandListReminders,
							},
						},
						[]bot.KeyboardButton{
							bot.KeyboardButton{
								Text: CommandCancel,
							},
						},
						[]bot.KeyboardButton{
							bot.KeyboardButton{
								Text: CommandHelp,
							},
						},
					},
				},
			}

			if update.Message.HasText() {
				txt := *update.Message.Text

				if strings.HasPrefix(txt, CommandStart) { // /start
					message = MessageUsage
				} else if strings.HasPrefix(txt, CommandListReminders) {
					reminders := db.UndeliveredQueueItems(chatId)
					if len(reminders) > 0 {
						for _, r := range reminders {
							message += fmt.Sprintf("%d. \"%s\" @%s\n", r.Id, r.Message, r.FireOn.Format("2006.1.2 15:04"))
						}
					} else {
						message = MessageNoReminders
					}
				} else if strings.HasPrefix(txt, CommandCancel) {
					reminders := db.UndeliveredQueueItems(chatId)
					if len(reminders) > 0 {
						// inline keyboards for canceling reminder
						buttons := [][]bot.InlineKeyboardButton{}
						for _, r := range reminders {
							buttons = append(buttons, []bot.InlineKeyboardButton{
								bot.InlineKeyboardButton{
									Text:         fmt.Sprintf("%d. %s", r.Id, r.Message),
									CallbackData: fmt.Sprintf("%s %d", CommandCancel, r.Id),
								},
							})
						}
						buttons = append(buttons, []bot.InlineKeyboardButton{
							bot.InlineKeyboardButton{
								Text:         MessageCancel,
								CallbackData: CommandCancel,
							},
						})
						options["reply_markup"] = bot.InlineKeyboardMarkup{
							InlineKeyboard: buttons,
						}
						message = MessageCancelWhat
					} else {
						message = MessageNoReminders
					}
				} else if strings.HasPrefix(txt, CommandHelp) {
					message = MessageUsage
				} else {
					if when, what, err := parseMessage(txt); err == nil {
						if db.Enqueue(chatId, update.Message.MessageId, txt, when) {
							message = fmt.Sprintf(MessageResponseFormat,
								username,
								when.Format("2006년 1월 2일 15:04"),
								what,
							)
						} else {
							message = fmt.Sprintf(MessageSaveFailedFormat, txt)
						}
					} else {
						message = fmt.Sprintf(MessageParseFailedFormat, err)
					}
				}
			}

			// send message
			if len(message) <= 0 {
				message = MessageError
			}
			if sent := b.SendMessage(chatId, &message, options); !sent.Ok {
				log.Printf("*** failed to send message: %s\n", *sent.Description)
			}
		} else if update.HasCallbackQuery() {
			processCallbackQuery(b, update)
		}
	} else {
		log.Printf("*** error while receiving update (%s)\n", err.Error())
	}
}

// process incoming callback query
func processCallbackQuery(b *bot.Bot, update bot.Update) bool {
	// process result
	result := false

	query := *update.CallbackQuery
	txt := *query.Data

	var message string
	if strings.HasPrefix(txt, CommandCancel) {
		if txt == CommandCancel {
			message = MessageCommandCanceled
		} else {
			cancelParam := strings.TrimSpace(strings.Replace(txt, CommandCancel, "", 1))
			if queueId, err := strconv.Atoi(cancelParam); err == nil {
				if db.DeleteQueueItem(query.Message.Chat.Id, int64(queueId)) {
					message = fmt.Sprintf(MessageReminderCanceledFormat, queueId)
				} else {
					log.Printf("*** Failed to delete reminder\n")

					message = MessageError
				}
			} else {
				log.Printf("*** Unprocessable callback query: %s\n", txt)

				message = MessageError
			}
		}
	} else {
		log.Printf("*** Unprocessable callback query: %s\n", txt)

		message = MessageError
	}

	// answer callback query
	if apiResult := b.AnswerCallbackQuery(query.Id, map[string]interface{}{"text": message}); apiResult.Ok {
		// edit message and remove inline keyboards
		options := map[string]interface{}{
			"chat_id":    query.Message.Chat.Id,
			"message_id": query.Message.MessageId,
		}
		if apiResult := b.EditMessageText(&message, options); apiResult.Ok {
			result = true
		} else {
			log.Printf("*** Failed to edit message text: %s\n", *apiResult.Description)

			db.LogError(fmt.Sprintf("failed to edit message text: %s", *apiResult.Description))
		}
	} else {
		log.Printf("*** Failed to answer callback query: %+v\n", query)

		db.LogError(fmt.Sprintf("failed to answer callback query: %+v", query))
	}

	return result
}

func parseMessage(message string) (when time.Time, what string, err error) {
	what = fmt.Sprintf("%s", message) // XXX - edit this?

	var hour, minute int
	if when, err = lkdp.ExtractDate(message, true); err == nil {
		if hour, minute, _, err = lkdp.ExtractTime(message, false); err != nil {
			hour, minute = 8, 0 // XXX - 08:00 as default
		}
		when = when.Add(time.Duration(hour) * time.Hour).Add(time.Duration(minute) * time.Minute)
	} else {
		if hour, minute, _, err = lkdp.ExtractTime(message, false); err == nil {
			now := time.Now()
			when = time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, _location)
		} else {
			return time.Time{}, "", fmt.Errorf(MessageNoDateTime)
		}
	}

	return when, what, nil
}

func main() {
	// monitor queue
	monitor := time.NewTicker(time.Duration(_monitorIntervalSeconds) * time.Second)
	log.Printf("Starting monitoring queue...\n")
	go func(client *bot.Bot) {
		for {
			select {
			case <-monitor.C:
				processQueue(client)
			}
		}
	}(telegram)

	// get info about this bot
	if me := telegram.GetMe(); me.Ok {
		log.Printf("Starting bot: @%s (%s)\n", *me.Result.Username, *me.Result.FirstName)

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
