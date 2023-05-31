package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	lkdp "github.com/meinside/lazy-korean-date-parser-go"
	bot "github.com/meinside/telegram-bot-go"
	"github.com/meinside/telegram-bot-reminder/database"
	"github.com/meinside/version-go"
)

const (
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
	messageUsageFormat            = `사용법:

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

* 버전: %[1]s

* 문의:
https://github.com/meinside/telegram-bot-reminder
`
)

var _location *time.Location

// return usable message value from given update
func messageFromUpdate(update bot.Update) *bot.Message {
	if update.HasMessage() {
		return update.Message
	}
	if update.HasEditedMessage() {
		return update.EditedMessage
	}

	return nil
}

// check if given update is allowed
func isAllowed(conf config, update bot.Update, username *string) bool {
	if !conf.RestrictUsers {
		return true
	}

	if username == nil {
		return false
	}

	message := messageFromUpdate(update)
	if message == nil {
		return false
	}

	if username := update.Message.From.Username; username != nil {
		for _, v := range conf.AllowedUserIDs {
			if v == *username {
				return true
			}
		}
	}

	return false
}

func monitorQueue(monitor *time.Ticker, client *bot.Bot, conf config, db *database.Database) {
	for range monitor.C {
		processQueue(client, conf, db)
	}
}

func processQueue(client *bot.Bot, conf config, db *database.Database) {
	if queue, err := db.DeliverableQueueItems(conf.MaxNumTries); err == nil {
		logDebug(conf, "checking queue: %d items...", len(queue))

		for _, q := range queue {
			go func(q database.QueueItem) {
				message := q.Message

				var sent bot.APIResponse[bot.Message]

				// if it is a message with a file,
				if q.FileID != nil && *q.FileType != "" {
					switch *q.FileType {
					case database.FileTypeDocument:
						sent = client.SendDocument(
							q.ChatID,
							bot.InputFileFromFileID(*q.FileID),
							bot.OptionsSendDocument{}.
								SetReplyMarkup(defaultReplyMarkup()).
								SetReplyToMessageID(q.MessageID).
								SetCaption(message))
					case database.FileTypeAudio:
						sent = client.SendAudio(
							q.ChatID,
							bot.InputFileFromFileID(*q.FileID),
							bot.OptionsSendAudio{}.
								SetReplyMarkup(defaultReplyMarkup()).
								SetReplyToMessageID(q.MessageID).
								SetCaption(message))
					case database.FileTypePhoto:
						sent = client.SendPhoto(
							q.ChatID,
							bot.InputFileFromFileID(*q.FileID),
							bot.OptionsSendPhoto{}.
								SetReplyMarkup(defaultReplyMarkup()).
								SetReplyToMessageID(q.MessageID).
								SetCaption(message))
					case database.FileTypeSticker:
						sent = client.SendSticker(
							q.ChatID,
							bot.InputFileFromFileID(*q.FileID),
							bot.OptionsSendSticker{}.
								SetReplyMarkup(defaultReplyMarkup()).
								SetReplyToMessageID(q.MessageID))
					case database.FileTypeVideo:
						sent = client.SendVideo(
							q.ChatID,
							bot.InputFileFromFileID(*q.FileID),
							bot.OptionsSendVideo{}.
								SetReplyMarkup(defaultReplyMarkup()).
								SetReplyToMessageID(q.MessageID).
								SetCaption(message))
					case database.FileTypeVoice:
						sent = client.SendVoice(
							q.ChatID,
							bot.InputFileFromFileID(*q.FileID),
							bot.OptionsSendVoice{}.
								SetReplyMarkup(defaultReplyMarkup()).
								SetReplyToMessageID(q.MessageID).
								SetCaption(message))
					}
				} else {
					// if it is just a message,
					sent = client.SendMessage(
						q.ChatID,
						message,
						bot.OptionsSendMessage{}.
							SetReplyMarkup(defaultReplyMarkup()).
							SetReplyToMessageID(q.MessageID))
				}

				if sent.Ok {
					// mark as delivered
					if _, err := db.MarkQueueItemAsDelivered(q.ChatID, q.ID); err != nil {
						logError(db, "failed to mark chat id: %d, queue id: %d (%s)", q.ChatID, q.ID, err)
					}
				} else {
					logError(db, "failed to send reminder: %s", *sent.Description)
				}

				// increase num tries
				if _, err := db.IncreaseNumTries(q.ChatID, q.ID); err != nil {
					logError(db, "failed to increase num tries for chat id: %d, queue id: %d (%s)", q.ChatID, q.ID, err)
				}
			}(q)
		}
	} else {
		logError(db, "failed to process queue: %s", err)
	}
}

// process polled updates
func processUpdate(b *bot.Bot, conf config, db *database.Database, update bot.Update, err error) {
	if err == nil {
		if update.HasMessage() || update.HasEditedMessage() {
			message := messageFromUpdate(update)
			username := message.From.Username

			if !isAllowed(conf, update, username) {
				if username != nil {
					logError(db, "id not allowed: %s", *username)
				}

				return
			}

			chatID := message.Chat.ID

			// 'is typing...'
			b.SendChatAction(chatID, bot.ChatActionTyping, bot.OptionsSendChatAction{})

			msg := ""
			options := bot.OptionsSendMessage{}.
				SetReplyMarkup(defaultReplyMarkup())

			if message.HasText() { // text
				txt := *message.Text

				if strings.HasPrefix(txt, commandStart) { // /start
					msg = usageMessage()
				} else if strings.HasPrefix(txt, commandListReminders) {
					if reminders, err := db.UndeliveredQueueItems(chatID); err == nil {
						if len(reminders) > 0 {
							format := fmt.Sprintf("%s\n", messageListItemFormat)
							for _, r := range reminders {
								msg += fmt.Sprintf(format, r.FireOn.In(_location).Format(defaultDatetimeFormat), r.Message)
							}
						} else {
							msg = messageNoReminders
						}
					} else {
						logError(db, "failed to process %s: %s", commandListReminders, err)
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
								{
									Text:         messageCancel,
									CallbackData: &cancel,
								},
							})

							// options
							options.SetReplyMarkup(bot.InlineKeyboardMarkup{
								InlineKeyboard: buttons,
							})

							msg = messageCancelWhat
						} else {
							msg = messageNoReminders
						}
					} else {
						logError(db, "failed to process %s: %s", commandCancel, err)
					}
				} else if strings.HasPrefix(txt, commandHelp) {
					msg = usageMessage()
				} else {
					if whens, _, err := parseMessage(txt); err == nil {
						if len(whens) == 1 {
							when := whens[0]

							if _, err := db.Enqueue(chatID, update.Message.MessageID, txt, nil, nil, when); err == nil {
								msg = fmt.Sprintf(messageResponseFormat,
									when.In(_location).Format(defaultDatetimeFormat),
									txt,
								)
							} else {
								msg = fmt.Sprintf(messageSaveFailedFormat, txt, err)
							}
						} else {
							if _, err := db.SaveTemporaryMessage(chatID, update.Message.MessageID, txt, nil, nil); err == nil {
								msg = messageSelectWhat

								// options for inline keyboards
								options.SetReplyMarkup(bot.InlineKeyboardMarkup{
									InlineKeyboard: datetimeButtonsForCallbackQuery(whens, chatID, update.Message.MessageID),
								})
							} else {
								msg = messageError
							}
						}
					} else {
						msg = fmt.Sprintf(messageParseFailedFormat, err)
					}
				}
			} else {
				processOthers(b, conf, db, message)
				return
			}

			// send message
			if len(msg) <= 0 {
				msg = messageError
			}
			if sent := b.SendMessage(chatID, msg, options); !sent.Ok {
				logError(db, "failed to send message: %s", *sent.Description)
			}
		} else if update.HasCallbackQuery() {
			processCallbackQuery(b, conf, db, update)
		}
	} else {
		logError(db, "error while receiving update (%s)", err.Error())
	}
}

// process incoming callback query
func processCallbackQuery(b *bot.Bot, conf config, db *database.Database, update bot.Update) bool {
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
						logError(db, "failed to delete reminder: %s", err)
					}
				} else {
					logError(db, "failed to get reminder: %s", err)
				}
			} else {
				logError(db, "unprocessable callback query: %s", data)
			}
		}
	} else if strings.HasPrefix(data, commandLoad) {
		params := strings.Split(strings.TrimSpace(strings.Replace(data, commandLoad, "", 1)), "/")

		if len(params) >= 3 {
			if chatID, err := strconv.ParseInt(params[0], 10, 64); err == nil {
				if messageID, err := strconv.ParseInt(params[1], 10, 64); err == nil {
					if saved, err := db.LoadTemporaryMessage(chatID, messageID); err == nil {
						if when, err := time.ParseInLocation(defaultDatetimeFormat, params[2], _location); err == nil {
							if _, err := db.Enqueue(chatID, messageID, saved.Message, saved.FileID, saved.FileType, when); err == nil {
								message = fmt.Sprintf(messageResponseFormat,
									when.In(_location).Format(defaultDatetimeFormat),
									saved.Message,
								)

								// delete temporary message
								if _, err := db.DeleteTemporaryMessage(chatID, messageID); err != nil {
									logError(db, "failed to delete temporary message: %s", err)
								}
							} else {
								message = fmt.Sprintf(messageSaveFailedFormat, saved.Message, err)
							}
						} else {
							logError(db, "failed to parse time: %s", err)
						}
					} else {
						logError(db, "failed to load temporary message with chat id: %d, message id: %d", chatID, messageID)
					}
				} else {
					logError(db, "failed to convert message id: %s", err)
				}
			} else {
				logError(db, "failed to convert chat id: %s", err)
			}
		} else {
			logError(db, "malformed inline keyboard data: %s", data)
		}
	} else {
		logError(db, "unprocessable callback query: %s", data)
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
			logError(db, "failed to edit message text: %s", *apiResult.Description)

			db.LogError(fmt.Sprintf("failed to edit message text: %s", *apiResult.Description))
		}
	} else {
		logError(db, "failed to answer callback query: %+v", query)

		db.LogError(fmt.Sprintf("failed to answer callback query: %+v", query))
	}

	return result
}

// process non-text messages
func processOthers(b *bot.Bot, conf config, db *database.Database, message *bot.Message) (result bool) {
	result = false

	chatID := message.Chat.ID
	username := *message.From.Username

	var msg string

	if message.HasDocument() { // file
		fileID := message.Document.FileID

		if message.HasCaption() {
			txt := *message.Caption

			options := bot.OptionsSendMessage{}.
				SetReplyMarkup(defaultReplyMarkup())

			if whens, _, err := parseMessage(txt); err == nil {
				typ := database.FileTypeDocument

				if len(whens) == 1 {
					when := whens[0]

					// enqueue received file
					if _, err := db.Enqueue(chatID, message.MessageID, txt, &fileID, &typ, when); err == nil {
						msg = fmt.Sprintf(messageWillSendBackFileFormat,
							username,
							"file",
							when.In(_location).Format(defaultDatetimeFormat),
						)

						result = true
					} else {
						msg = fmt.Sprintf(messageSaveFailedFormat, txt, err)
					}
				} else {
					if _, err := db.SaveTemporaryMessage(chatID, message.MessageID, txt, &fileID, &typ); err == nil {
						msg = messageSelectWhat

						// options for inline keyboards
						options.SetReplyMarkup(bot.InlineKeyboardMarkup{
							InlineKeyboard: datetimeButtonsForCallbackQuery(whens, chatID, message.MessageID),
						})

						result = true
					} else {
						msg = messageError
					}
				}
			} else {
				msg = fmt.Sprintf(messageParseFailedFormat, err)
			}

			if sent := b.SendMessage(chatID, msg, options); !sent.Ok {
				logError(db, "failed to send message: %s", *sent.Description)
			}
		} else {
			// send received file back immediately
			options := bot.OptionsSendDocument{}.
				SetReplyMarkup(defaultReplyMarkup()).
				SetCaption(messageSendingBackFile)

			if sent := b.SendDocument(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
				result = true
			} else {
				logError(db, "failed to send document back: %s", *sent.Description)
			}
		}
	} else if message.HasAudio() { // audio
		fileID := message.Audio.FileID

		options := bot.OptionsSendMessage{}.
			SetReplyMarkup(defaultReplyMarkup())

		if message.HasCaption() {
			txt := *message.Caption

			if whens, _, err := parseMessage(txt); err == nil {
				typ := database.FileTypeAudio

				if len(whens) == 1 {
					when := whens[0]

					// enqueue received file
					if _, err := db.Enqueue(chatID, message.MessageID, txt, &fileID, &typ, when); err == nil {
						msg = fmt.Sprintf(messageWillSendBackFileFormat,
							username,
							"audio",
							when.In(_location).Format(defaultDatetimeFormat),
						)

						result = true
					} else {
						msg = fmt.Sprintf(messageSaveFailedFormat, txt, err)
					}
				} else {
					if _, err := db.SaveTemporaryMessage(chatID, message.MessageID, txt, &fileID, &typ); err == nil {
						msg = messageSelectWhat

						// options for inline keyboards
						options.SetReplyMarkup(bot.InlineKeyboardMarkup{
							InlineKeyboard: datetimeButtonsForCallbackQuery(whens, chatID, message.MessageID),
						})

						result = true
					} else {
						msg = messageError
					}
				}
			} else {
				msg = fmt.Sprintf(messageParseFailedFormat, err)
			}

			if sent := b.SendMessage(chatID, msg, options); !sent.Ok {
				logError(db, "failed to send message: %s", *sent.Description)
			}
		} else {
			options := bot.OptionsSendAudio{}.
				SetReplyMarkup(defaultReplyMarkup()).
				SetCaption(messageSendingBackFile)

			// send received file back immediately
			if sent := b.SendAudio(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
				result = true
			} else {
				logError(db, "failed to send audio back: %s", *sent.Description)
			}
		}
	} else if message.HasPhoto() { // photo
		photo := message.LargestPhoto()
		fileID := photo.FileID

		if message.HasCaption() {
			txt := *message.Caption

			options := bot.OptionsSendMessage{}.
				SetReplyMarkup(defaultReplyMarkup())

			if whens, _, err := parseMessage(txt); err == nil {
				typ := database.FileTypePhoto

				if len(whens) == 1 {
					when := whens[0]

					// enqueue received file
					if _, err := db.Enqueue(chatID, message.MessageID, txt, &fileID, &typ, when); err == nil {
						msg = fmt.Sprintf(messageWillSendBackFileFormat,
							username,
							"image",
							when.In(_location).Format(defaultDatetimeFormat),
						)

						result = true
					} else {
						msg = fmt.Sprintf(messageSaveFailedFormat, txt, err)
					}
				} else {
					if _, err := db.SaveTemporaryMessage(chatID, message.MessageID, txt, &fileID, &typ); err == nil {
						msg = messageSelectWhat

						// options for inline keyboards
						options.SetReplyMarkup(bot.InlineKeyboardMarkup{
							InlineKeyboard: datetimeButtonsForCallbackQuery(whens, chatID, message.MessageID),
						})

						result = true
					} else {
						msg = messageError
					}
				}
			} else {
				msg = fmt.Sprintf(messageParseFailedFormat, err)
			}

			if sent := b.SendMessage(chatID, msg, options); !sent.Ok {
				logError(db, "failed to send message: %s", *sent.Description)
			}
		} else {
			options := bot.OptionsSendPhoto{}.
				SetReplyMarkup(defaultReplyMarkup()).
				SetCaption(messageSendingBackFile)

			// send received file back immediately
			if sent := b.SendPhoto(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
				result = true
			} else {
				logError(db, "failed to send photo back: %s", *sent.Description)
			}
		}
	} else if message.HasSticker() { // sticker (has no caption)
		fileID := message.Sticker.FileID

		options := bot.OptionsSendSticker{}.
			SetReplyMarkup(defaultReplyMarkup())

		// send received file back immediately
		if sent := b.SendSticker(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
			result = true
		} else {
			logError(db, "failed to send sticker back: %s", *sent.Description)
		}
	} else if message.HasVideo() { // video
		fileID := message.Video.FileID

		if message.HasCaption() {
			txt := *message.Caption

			options := bot.OptionsSendMessage{}.
				SetReplyMarkup(defaultReplyMarkup())

			if whens, _, err := parseMessage(txt); err == nil {
				typ := database.FileTypeVideo

				if len(whens) == 1 {
					when := whens[0]

					// enqueue received file
					if _, err := db.Enqueue(chatID, message.MessageID, txt, &fileID, &typ, when); err == nil {
						msg = fmt.Sprintf(messageWillSendBackFileFormat,
							username,
							"image",
							when.In(_location).Format(defaultDatetimeFormat),
						)

						result = true
					} else {
						msg = fmt.Sprintf(messageSaveFailedFormat, txt, err)
					}
				} else {
					if _, err := db.SaveTemporaryMessage(chatID, message.MessageID, txt, &fileID, &typ); err == nil {
						msg = messageSelectWhat

						// options for inline keyboards
						options.SetReplyMarkup(bot.InlineKeyboardMarkup{
							InlineKeyboard: datetimeButtonsForCallbackQuery(whens, chatID, message.MessageID),
						})

						result = true
					} else {
						msg = messageError
					}
				}
			} else {
				msg = fmt.Sprintf(messageParseFailedFormat, err)
			}

			if sent := b.SendMessage(chatID, msg, options); !sent.Ok {
				logError(db, "failed to send message: %s", *sent.Description)
			}
		} else {
			// send received file back immediately
			options := bot.OptionsSendVideo{}.
				SetReplyMarkup(defaultReplyMarkup()).
				SetCaption(messageSendingBackFile)
			if sent := b.SendVideo(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
				result = true
			} else {
				logError(db, "failed to send video back: %s", *sent.Description)
			}
		}
	} else if message.HasVoice() { // voice (has no caption)
		fileID := message.Voice.FileID

		// send received file back immediately
		options := bot.OptionsSendVoice{}.
			SetReplyMarkup(defaultReplyMarkup()).
			SetCaption(messageSendingBackFile)
		if sent := b.SendVoice(chatID, bot.InputFileFromFileID(fileID), options); sent.Ok {
			result = true
		} else {
			logError(db, "failed to send voice back: %s", *sent.Description)
		}
	}

	return result
}

func parseMessage(message string) (candidates []time.Time, what string, err error) {
	now := time.Now()
	what = message // XXX - edit this?

	var dates map[string]time.Time
	var times map[string]lkdp.Hms
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
func datetimeButtonsForCallbackQuery(times []time.Time, chatID int64, messageID int64) [][]bot.InlineKeyboardButton {
	// datetime buttons
	keys := make(map[string]string)
	for _, w := range times {
		keys[w.In(_location).Format(defaultDatetimeFormat)] = fmt.Sprintf("%s %d/%d/%s", commandLoad, chatID, messageID, w.In(_location).Format(defaultDatetimeFormat))
	}
	buttons := bot.NewInlineKeyboardButtonsAsRowsWithCallbackData(keys)

	// add cancel button
	cancel := commandCancel
	buttons = append(buttons, []bot.InlineKeyboardButton{
		{
			Text:         messageCancel,
			CallbackData: &cancel,
		},
	})

	return buttons
}

// default reply markup
func defaultReplyMarkup() bot.ReplyKeyboardMarkup {
	return bot.ReplyKeyboardMarkup{ // show keyboards
		Keyboard: [][]bot.KeyboardButton{
			bot.NewKeyboardButtons(commandListReminders, commandCancel, commandHelp),
		},
		ResizeKeyboard: true,
	}
}

// generate usage message string
func usageMessage() string {
	return fmt.Sprintf(messageUsageFormat, version.Minimum())
}

// run bot
func runBot(conf config, db *database.Database) {
	if conf.MonitorIntervalSeconds <= 0 {
		conf.MonitorIntervalSeconds = 10
	}

	if conf.TelegramIntervalSeconds <= 0 {
		conf.TelegramIntervalSeconds = 1
	}

	if conf.MaxNumTries < 0 {
		conf.MaxNumTries = 10
	}

	telegram := bot.NewClient(conf.TelegramAPIToken)
	telegram.Verbose = conf.IsVerbose

	_location, _ = time.LoadLocation("Local")

	// monitor queue
	logInfo("starting monitoring queue...")
	go monitorQueue(
		time.NewTicker(time.Duration(conf.MonitorIntervalSeconds)*time.Second),
		telegram,
		conf,
		db,
	)

	// get info about this bot
	if me := telegram.GetMe(); me.Ok {
		if setCommands := telegram.SetMyCommands([]bot.BotCommand{
			{Command: commandListReminders, Description: descriptionCommandListReminders},
			{Command: commandCancel, Description: descriptionCommandCancel},
			{Command: commandHelp, Description: descriptionCommandHelp},
		}, nil); !setCommands.Ok {
			logError(db, "failed to set bot commands")
		}

		logInfo("starting bot: @%s (%s)", *me.Result.Username, me.Result.FirstName)

		// delete webhook (getting updates will not work when wehbook is set up)
		if unhooked := telegram.DeleteWebhook(true); unhooked.Ok {
			// wait for new updates
			telegram.StartMonitoringUpdates(0, conf.TelegramIntervalSeconds, func(b *bot.Bot, update bot.Update, err error) {
				processUpdate(b, conf, db, update, err)
			})
		} else {
			logErrorAndDie(db, "failed to delete webhook")
		}
	} else {
		logErrorAndDie(db, "failed to get info of the bot")
	}
}
