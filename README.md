# **(archived)**

[이거](https://github.com/meinside/telegram-reminder-bot)로 갈아탐.





# Simple Reminder Bot (Telegram)

간단히 사용 가능한 reminder bot.

날짜나 시간이 포함된 메시지를 수신하면

그 날짜/시간을 인식하여 해당 시간에 메시지를 다시 돌려보내줌.

파일(사진, 동영상 등) 업로드 시에는 caption으로 메시지를 작성하면 되며,

caption을 추가하지 않으면 즉시 메시지를 돌려보냄.

메시지 내용에 날짜/시간이 여러번 등장할 경우에는 그 중에서 선택하도록 해줌.

## 지원 Database

- [x] SQLite

## install

```bash
$ go get -d github.com/meinside/telegram-bot-reminder
$ cd $GOPATH/src/github.com/meinside/telegram-bot-reminder
```

## configure

샘플로 들어있는 config.json.sample을 config.json으로 복사, 고쳐서 사용

```bash
$ cp config.json.sample config.json
$ vi config.json
```

**telegram_api_token** 값을 본인의 telegram bot api token으로 교체하여 사용할 것.

## run

### A. build and run

```bash
$ go build
$ ./telegram-bot-reminder
```

### B. with docker-compose

```bash
$ docker-compose build
```

로 build 후,

```bash
$ docker-compose up -d
```

등으로 실행.

## 메시지 용례

* "내일 이 메시지 다시 보내줄래?" => 다음날 08:00에 알림
* "18:30 알림" => 오늘 18:30에 알림
* "2016-12-31 오후 11시에 신년 타종행사 보라고 알려다오" => 2016-12-31 23:00에 알림

## license

MIT

