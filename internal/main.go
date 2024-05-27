package app

import (
	"context"
	"fmt"
	"log"

	// "os"
	"strconv"

	// "strings"

	"database/sql"

	//db driver
	"embed"

	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"

	"os/signal"
	"syscall"
	"time"

	"github.com/tinkoff/invest-api-go-sdk/investgo"
	// pb "github.com/tinkoff/invest-api-go-sdk/proto"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// -----vars-----
var (
	// инициализируем переменные, как глобальные в данном пакете,
	// чтобы отредактировать их в функциях

	logger *zap.SugaredLogger
	cancel context.CancelFunc
	// conn *pgx.Conn
	ctx context.Context

	configTemplate = investgo.Config{
		// EndPoint - Для работы с реальным контуром и контуром песочницы нужны разные эндпоинты.
		// По умолчанию = sandbox-invest-public-api.tinkoff.ru:443
		//https://tinkoff.github.io/investAPI/url_difference/
		EndPoint: "sandbox-invest-public-api.tinkoff.ru:443",
		// AppName - Название вашего приложения, по умолчанию = tinkoff-api-go-sdk
		AppName: "invest-api-go-sdk",
		// DisableResourceExhaustedRetry - Если true, то сдк не пытается ретраить, после получения ошибки об исчерпывании
		// лимита запросов, если false, то сдк ждет нужное время и пытается выполнить запрос снова. По умолчанию = false
		DisableResourceExhaustedRetry: false,
		// MaxRetries - Максимальное количество попыток переподключения, по умолчанию = 3
		// (если указать значение 0 это не отключит ретраи, для отключения нужно прописать DisableAllRetry = true)
		MaxRetries: 3,
	}

	flagAcс   bool
	flagTrade bool
	s         *Service

	msgAcc = `
	Отправьте свой API токен. Где взять токен аутентификации? В разделе инвестиций вашего [личного кабинета tinkoff](https://www.tinkoff.ru/invest/). Далее:
	
	— Перейдите в [настройки](https://www.tinkoff.ru/invest/settings/)
	— Проверьте, что функция “Подтверждение сделок кодом” отключена
	— Выпустите токен (если не хотите через API выдавать торговые поручения, то надо выпустить токен "только для чтения")  
	— Скопируйте токен и сохраните, токен отображается только один раз, просмотреть его позже не получится, тем не менее вы можете выпускать неограниченное количество токенов
	`

	menuKeyboard = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Аккаунт", "Аккаунт")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Биржа", "Биржа")),
	)
)

// var psqlInfoOld = fmt.Sprintf("host=%s user=%s password=%s dbname=%s sslmode=disable",
// 	os.Getenv("POSTGRES_HOSTNAME"),
// 	os.Getenv("POSTGRES_USER"),
// 	os.Getenv("POSTGRES_PASSWORD"),
// 	os.Getenv("POSTGRES_DB"),
// )

var psqlInfo = fmt.Sprintf("host=%s user=%s password=%s dbname=%s sslmode=disable",
	"localhost",
	"postgres",
	"postgres",
	"postgres",
)

const (
	dbAccountId = "accountid"
	dbTGID      = "tgid"
	dbAPIToken  = "apitoken"
)

// Service is the backend DB/REST api struct
type Service struct {
}

func (s *Service) getDatabase() (*sql.DB, error) {
	return sql.Open("postgres", psqlInfo)
}

// AddUserIntoDatabase insert new user into DB
func (s *Service) AddUserIntoDatabase(TGID int64, APIToken string) (newID int, err error) {
	db, err := s.getDatabase()
	if err != nil {
		logger.Fatal(err)
		return
	}
	defer db.Close()

	if err = db.Ping(); err != nil {
		logger.Fatal(err)
		return
	}

	rowStmt, err := db.Prepare("SELECT MAX(id) AS id FROM users")
	if err != nil {
		logger.Fatal(err)
		return
	}
	defer rowStmt.Close()

	// get the last order id

	var id sql.NullInt32
	if err = rowStmt.QueryRow().Scan(&id); err != nil {
		logger.Fatal(err)
		return
	}
	if id.Valid {
		newID = int(id.Int32) + 1
	} else {
		newID = 1
	}

	// write each order line as a row
	insertStmt, err := db.Prepare("INSERT INTO users (id, tgid, apitoken, accountid) values ($1, $2, $3, $4)")
	if err != nil {
		logger.Fatal(err)
		return
	}
	defer insertStmt.Close()

	AccountId := getAccountId(APIToken)
	logger.Info(AccountId)
	if _, err = insertStmt.Exec(newID, TGID, APIToken, AccountId); err != nil {
		logger.Fatal(err)
	}
	// var itemCount int
	// for _, line := range newUser.Lines {
	// 	itemCount += line.Quantity
	// 	if _, err = insertStmt.Exec(newID, line.ProductID, line.Quantity); err != nil {
	// 		log.Println(err)
	// 	}
	// }

	listUsersAll()

	log.Printf("User #%d (%d tgid) added\n", newID, TGID)
	return
}

func listUsersAll() {
	db, err := s.getDatabase()
	if err != nil {
		log.Println(err)
		return
	}
	defer db.Close()

	// Запрос на выборку данных из таблицы
	rows, err := db.Query("SELECT * FROM users")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	// Получение имен колонок
	cols, err := rows.Columns()
	if err != nil {
		log.Fatal(err)
	}

	// Создание слайса для значений
	vals := make([]interface{}, len(cols))
	for i := range vals {
		vals[i] = new(interface{})
	}

	// Итерация по строкам результатов
	for rows.Next() {
		err = rows.Scan(vals...)
		if err != nil {
			log.Fatal(err)
		}

		// Вывод значений
		for i, col := range cols {
			val := *(vals[i].(*interface{}))
			fmt.Printf("%s: %v\n", col, val)
		}
		fmt.Println("---------------")
	}

	// Проверка на ошибки после итерации
	if err = rows.Err(); err != nil {
		log.Fatal(err)
	}
}

func (s *Service) getSmthFromDB(TGID int64, a string) (res string) {
	db, err := s.getDatabase()
	if err != nil {
		log.Println(err)
		return
	}
	defer db.Close()

	sqlStatement := `SELECT $1 FROM users WHERE tgid=$2;`
	row := db.QueryRow(sqlStatement, a, TGID)
	switch err := row.Scan(&res); err {
	case sql.ErrNoRows:
		fmt.Println("No rows were returned!")
	case nil:
		return res
	}
	return ""
}

func getAccountId(APIToken string) (AccountID string) {
	ctx, cancel = signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
	cfg := configTemplate
	cfg.Token = APIToken

	client, err := investgo.NewClient(ctx, cfg, logger)
	if err != nil {
		logger.Fatalf("client creating error %v", err.Error())
	}
	defer closeClient(*client)

	// сервис песочницы нужен лишь для управления счетами песочнцы и пополнения баланса
	// остальной функционал доступен через обычные сервисы, но с эндпоинтом песочницы
	// для этого в конфиге сдк EndPoint = sandbox-invest-public-api.tinkoff.ru:443
	sandboxService := client.NewSandboxServiceClient()
	// открыть счет в песочнице можно через Kreya или BloomRPC, просто указав его в конфиге
	// или следующим образом из кода
	accountsResp, err := sandboxService.GetSandboxAccounts()
	if err != nil {
		log.Fatalln(err.Error())
		// logger.Errorf(err.Error())
	} else {
		accs := accountsResp.GetAccounts()
		if len(accs) > 0 {
			// если счета есть, берем первый
			AccountID = accs[0].GetId()
		} else {
			// если открытых счетов нет
			openAccount, err := sandboxService.OpenSandboxAccount()
			if err != nil {
				logger.Errorf(err.Error())
			} else {
				AccountID = openAccount.GetAccountId()
			}
		}
	}
	return AccountID
}

// -----init-----
// init investgo, logger
func loadInit() {
	ctx, cancel = signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
	// сдк использует для внутреннего логирования investgo.Logger
	// для примера передадим uber.zap
	zapConfig := zap.NewDevelopmentConfig()
	zapConfig.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout(time.DateTime)
	zapConfig.EncoderConfig.TimeKey = "time"
	l, err := zapConfig.Build()
	logger = l.Sugar()

	if err != nil {
		log.Fatalf("logger creating error %v", err)
	}
}

//go:embed migrations/*.sql
var embedMigrations embed.FS

// init PostgreSQL
func (s *Service) dbInit() {

	goose.SetBaseFS(embedMigrations)
	db, err := s.getDatabase()
	if err != nil {
		log.Println(err)
		return
	}
	defer db.Close()
	fmt.Println("Successfully connected!")

	if err := goose.SetDialect("postgres"); err != nil {
		panic(err)
	}

	if err := goose.Up(db, "migrations"); err != nil {
		panic(err)
	}
	listUsersAll()
}

func closeClient(client investgo.Client) {
	logger.Infof("closing client connection")
	err := client.Stop()
	if err != nil {
		logger.Errorf("client shutdown error %v", err.Error())
	}
}

// functional
// Пополнить счёт на v рублей
func PayIn(TGID int64, v int64) string {
	AccountId := s.getSmthFromDB(TGID, dbAccountId)
	APIToken := s.getSmthFromDB(TGID, dbAPIToken)

	cfg := configTemplate
	cfg.AccountId = AccountId
	cfg.Token = APIToken

	client, err := investgo.NewClient(ctx, cfg, logger)
	if err != nil {
		logger.Fatalf("client creating error %v", err.Error())
	}
	defer closeClient(*client)

	sandboxService := client.NewSandboxServiceClient()
	payInResp, err := sandboxService.SandboxPayIn(&investgo.SandboxPayInRequest{
		AccountId: AccountId,
		Currency:  "RUB",
		Unit:      v,
		Nano:      0,
	})

	if err != nil {
		logger.Errorf(err.Error())
		return "err"
	} else {
		return fmt.Sprintf("На аккаунте песочницы %v текущий баланс = %v\n", AccountId, payInResp.GetBalance().ToFloat())
	}
}

func (s *Service) updateUser(TGID int64, APIToken string) error {
	db, err := s.getDatabase()
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec("UPDATE users SET apitoken=$1 WHERE tgid=$2", APIToken, TGID)
	return err
}

func startBot() {
	// api, _ := os.LookupEnv("TGAPI")
	api := "6520187946:AAHAh_nDEIUG2ZBsNfjnlKSubVGxve_sGpQ"
	// logger.Info(api)
	bot, err := tgbotapi.NewBotAPI(api)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = false

	log.Printf("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)
	for update := range updates {
		if update.Message != nil {
			if update.Message.Command() == "start" {
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Меню")
				msg.ReplyMarkup = menuKeyboard

				bot.Send(msg)
			} else if flagAcс {
				flagAcс = false
				// add user to db
				// if user in db -> отредактировать
				TGID := update.Message.Chat.ID
				APIToken := update.Message.Text
				if s.getSmthFromDB(TGID, dbAPIToken) != "" {
					s.updateUser(TGID, APIToken)
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Аккаунт успешно отредактирован!")
					bot.Send(msg)
				} else {
					s.AddUserIntoDatabase(TGID, APIToken)
					listUsersAll()
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Аккаунт успешно зарегестрирован в боте!")
					bot.Send(msg)
				}

				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Меню")
				msg.ReplyMarkup = menuKeyboard
				bot.Send(msg)
			} else if flagTrade {
				flagTrade = false
				a := s.getSmthFromDB(update.Message.Chat.ID, dbAPIToken)
				if a == "" {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Похоже, вы не добавили свой API токен в разделе 'Аккаунт'")
					bot.Send(msg)

					//возврат в меню
					msg = tgbotapi.NewMessage(update.Message.Chat.ID, "Меню")
					msg.ReplyMarkup = menuKeyboard
					bot.Send(msg)
				} else {
					if n, err := strconv.ParseInt(update.Message.Text, 10, 64); err == nil {
						sum := PayIn(update.Message.Chat.ID, n)
						logger.Info(sum)

						msg := tgbotapi.NewMessage(update.Message.Chat.ID, sum)
						bot.Send(msg)
						//возврат в меню
						msg = tgbotapi.NewMessage(update.Message.Chat.ID, "Меню")
						msg.ReplyMarkup = menuKeyboard
						bot.Send(msg)
					} else {
						logger.Error(update.Message.Text, "is not an integer.")
						msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Вы не отправили число, повторите попытку")
						bot.Send(msg)
						flagTrade = true
					}
				}

			}

		} else if update.CallbackQuery != nil {
			chatcallbackId := update.CallbackQuery.Message.Chat.ID
			callback := tgbotapi.NewCallback(update.CallbackQuery.ID, update.CallbackQuery.Data)
			if _, err := bot.Request(callback); err != nil {
				panic(err)
			}

			switch update.CallbackQuery.Data {
			case "Аккаунт":
				msg_edit := tgbotapi.NewEditMessageText(chatcallbackId, update.CallbackQuery.Message.MessageID, msgAcc)
				msg_edit.ParseMode = "Markdown"
				flagAcс = true
				if _, err := bot.Send(msg_edit); err != nil {
					panic(err)
				}

			case "Биржа":
				a := s.getSmthFromDB(chatcallbackId, dbAPIToken)
				if a == "" {
					msg_edit := tgbotapi.NewEditMessageText(chatcallbackId, update.CallbackQuery.Message.MessageID, "Похоже, вы не добавили свой API токен в разделе 'Аккаунт'")
					if _, err := bot.Send(msg_edit); err != nil {
						panic(err)
					}
					//возврат в меню
					msg := tgbotapi.NewMessage(chatcallbackId, "Меню")
					msg.ReplyMarkup = menuKeyboard
					bot.Send(msg)
				} else {
					msg_edit := tgbotapi.NewEditMessageText(chatcallbackId, update.CallbackQuery.Message.MessageID, "Отправьте количество рублей, на которое желаете пополнить свой баланс")
					flagTrade = true
					if _, err := bot.Send(msg_edit); err != nil {
						panic(err)
					}
				}

				// msg_edit := tgbotapi.NewEditMessageTextAndMarkup(chatcallbackId, update.CallbackQuery.Message.MessageID, "Аудио", audioKeyboard)
				// if _, err := bot.Send(msg_edit); err != nil {
				// 	panic(err)
				// }
			}
		}
	}
}

func Start() {
	loadInit()
	defer cancel()
	defer logger.Sync()
	// defer closeClient()
	service := Service{}

	service.dbInit()

	startBot()
	// пополняем счет песочницы на 100 000 рублей
	// PayIn(-600000)

	// далее вызываем нужные нам сервисы, используя счет, токен, и эндпоинт песочницы
	// создаем клиента для сервиса песочницы
	// instrumentsService := client.NewInstrumentsServiceClient()

	// var id string
	// instrumentResp, err := instrumentsService.FindInstrument("YNDX")
	// if err != nil {
	// 	logger.Errorf(err.Error())
	// } else {
	// 	instruments := instrumentResp.GetInstruments()
	// 	for _, instrument := range instruments {
	// 		if instrument.GetTicker() == "YNDX" {
	// 			id = instrument.GetUid()
	// 		}
	// 	}
	// }
	// ordersService := client.NewOrdersServiceClient()
	// buyResp, err := ordersService.Buy(&investgo.PostOrderRequestShort{
	// 	InstrumentId: id,
	// 	Quantity:     1,
	// 	Price:        nil,
	// 	AccountId:    AccId,
	// 	OrderType:    pb.OrderType_ORDER_TYPE_MARKET,
	// 	OrderId:      investgo.CreateUid(),
	// })
	// if err != nil {
	// 	logger.Errorf(err.Error())
	// 	fmt.Printf("msg = %v\n", investgo.MessageFromHeader(buyResp.GetHeader()))
	// } else {
	// 	fmt.Printf("order status = %v\n", buyResp.GetExecutionReportStatus().String())
	// }

	// operationsService := client.NewOperationsServiceClient()

	// positionsResp, err := operationsService.GetPositions(AccId)
	// if err != nil {
	// 	logger.Errorf(err.Error())
	// 	fmt.Printf("msg = %v\n", investgo.MessageFromHeader(buyResp.GetHeader()))
	// } else {
	// 	positions := positionsResp.GetSecurities()
	// 	for i, position := range positions {
	// 		fmt.Printf("position number %v, uid = %v\n", i, position.GetInstrumentUid())
	// 	}
	// }

	// sellResp, err := ordersService.Sell(&investgo.PostOrderRequestShort{
	// 	InstrumentId: id,
	// 	Quantity:     1,
	// 	Price:        nil,
	// 	AccountId:    AccId,
	// 	OrderType:    pb.OrderType_ORDER_TYPE_MARKET,
	// 	OrderId:      investgo.CreateUid(),
	// })
	// if err != nil {
	// 	logger.Errorf(err.Error())
	// } else {
	// 	fmt.Printf("order status = %v\n", sellResp.GetExecutionReportStatus().String())
	// }
}
