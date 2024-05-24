package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"

	// "strings"

	"database/sql"

	//db driver
	_ "github.com/lib/pq"

	// "github.com/jackc/pgx/v5"

	"os/signal"
	"syscall"
	"time"

	"github.com/tinkoff/invest-api-go-sdk/investgo"
	// pb "github.com/tinkoff/invest-api-go-sdk/proto"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)


// -----vars and structs-----
var (
	// инициализируем переменные, как глобальные в данном пакете,
	// чтобы отредактировать их в функциях
	client         *investgo.Client
	config         investgo.Config
	logger         *zap.SugaredLogger
	sandboxService *investgo.SandboxServiceClient
	cancel         context.CancelFunc
	conn *pgx.Conn
	ctx context.Context


	AccId string
	flagAcс bool
	flagTrade bool

	msgAcc = `
	Отправьте свой API токен. Где взять токен аутентификации? В разделе инвестиций вашего [личного кабинета tinkoff](https://www.tinkoff.ru/invest/). Далее:
	
	— Перейдите в [настройки](https://www.tinkoff.ru/invest/settings/)
	— Проверьте, что функция “Подтверждение сделок кодом” отключена
	— Выпустите токен (если не хотите через API выдавать торговые поручения, то надо выпустить токен "только для чтения")  
	— Скопируйте токен и сохраните, токен отображается только один раз, просмотреть его позже не получится, тем не менее вы можете выпускать неограниченное количество токенов
	`

	menuKeyboard = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Аккаунт", "Аккаунт")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Биржа", "Биржа"),),
	)
)

var psqlInfo = fmt.Sprintf("host=%s user=%s password=%s dbname=%s sslmode=disable",
	os.Getenv("POSTGRES_HOSTNAME"),
	os.Getenv("POSTGRES_USER"),
	os.Getenv("POSTGRES_PASSWORD"),
	os.Getenv("POSTGRES_DB"),
)

// Service is the backend DB/REST api struct
type Service struct {
}


func (s *Service) getDatabase() (*sql.DB, error) {
	return sql.Open("postgres", psqlInfo)
}

// StoreOrderIntoDatabase insert new order into DB
func (s *Service) AddUserIntoDatabase(newUser User) (newID int, err error) {
	db, err := s.getDatabase()
	if err != nil {
		log.Println(err)
		return
	}
	defer db.Close()

	if err = db.Ping(); err != nil {
		log.Println(err)
		return
	}

	rowStmt, err := db.Prepare("SELECT MAX(id) AS id FROM users")
	if err != nil {
		log.Println(err)
		return
	}
	defer rowStmt.Close()

	// get the last order id

	var id sql.NullInt32
	if err = rowStmt.QueryRow().Scan(&id); err != nil {
		log.Println(err)
		return
	}
	if id.Valid {
		newID = int(id.Int32) + 1
	} else {
		newID = 1
	}

	// write each order line as a row

	insertStmt, err := db.Prepare("INSERT INTO orders (id, tgid, apitoken, accountid) values (?, ?, ?, ?)")
	if err != nil {
		log.Println(err)
		return
	}
	defer insertStmt.Close()

	if _, err = insertStmt.Exec(newID, tgid, apitoken, accountid); err != nil {
		log.Println(err)
	}
	// var itemCount int
	// for _, line := range newUser.Lines {
	// 	itemCount += line.Quantity
	// 	if _, err = insertStmt.Exec(newID, line.ProductID, line.Quantity); err != nil {
	// 		log.Println(err)
	// 	}
	// }

	log.Printf("User #%d (%d tgid) added\n", newID, tgid)
	return
}

func getAccountId() (AccountID string){
	ctx, cancel = signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
	client, err := investgo.NewClient(ctx, config, logger)
	if err != nil {
		logger.Fatalf("client creating error %v", err.Error())
	}

	// сервис песочницы нужен лишь для управления счетами песочнцы и пополнения баланса
	// остальной функционал доступен через обычные сервисы, но с эндпоинтом песочницы
	// для этого в конфиге сдк EndPoint = sandbox-invest-public-api.tinkoff.ru:443
	sandboxService = client.NewSandboxServiceClient()

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
			AccId = accs[0].GetId()
		} else {
			// если открытых счетов нет
			openAccount, err := sandboxService.OpenSandboxAccount()
			if err != nil {
				logger.Errorf(err.Error())
			} else {
				AccId = openAccount.GetAccountId()
			}
			// запись в конфиг
			client.Config.AccountId = AccId
		}
	}
}


// -----init-----
// init investgo, logger
func loadInit() {
	// загружаем конфигурацию для сдк из .yaml файла
	config, err := investgo.LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("config loading error %v", err.Error())
	}
	var ctx context.Context
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
	// создаем клиента для investAPI, он позволяет создавать нужные сервисы и уже
	// через них вызывать нужные методы
	client, err = investgo.NewClient(ctx, config, logger)
	if err != nil {
		logger.Fatalf("client creating error %v", err.Error())
	}

	// сервис песочницы нужен лишь для управления счетами песочнцы и пополнения баланса
	// остальной функционал доступен через обычные сервисы, но с эндпоинтом песочницы
	// для этого в конфиге сдк EndPoint = sandbox-invest-public-api.tinkoff.ru:443
	sandboxService = client.NewSandboxServiceClient()

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
			AccId = accs[0].GetId()
		} else {
			// если открытых счетов нет
			openAccount, err := sandboxService.OpenSandboxAccount()
			if err != nil {
				logger.Errorf(err.Error())
			} else {
				AccId = openAccount.GetAccountId()
			}
			// запись в конфиг
			client.Config.AccountId = AccId
		}
	}
}

// init PostgreSQL
// func dbInit() {
	// psqlInfo := fmt.Sprintf("host=%s user=%s password=%s dbname=%s sslmode=disable",
	// 	os.Getenv("POSTGRES_HOSTNAME"),
	// 	os.Getenv("POSTGRES_USER"),
	// 	os.Getenv("POSTGRES_PASSWORD"),
	// 	os.Getenv("POSTGRES_DB"),
	// )
// 	var err error
// 	db, err = sql.Open("postgres", psqlInfo)
// 	if err != nil {
// 		panic(err)
// 	}

// 	err = db.Ping()
// 	if err != nil {
// 		panic(err)
// 	}

// 	fmt.Println("Successfully connected!")
// }
func dbInit() {
    url := fmt.Sprintf("postgres://%s:%s@%s:5432/%s",
	os.Getenv("POSTGRES_USER"),
	os.Getenv("POSTGRES_PASSWORD"),
	os.Getenv("POSTGRES_HOSTNAME"),
	os.Getenv("POSTGRES_DB"),
	)
	var err error
    conn, err = pgx.Connect(context.Background(), url)
	if err != nil {
		logger.Fatalf("Невозможно подключиться к базе данных: %v\n", err)
	}
	createTable()
	listUsers()
	logger.Info("Успешное подключение БД!")

	// q := db.New(conn)

	// author, err := q.GetAuthor(context.Background(), 1)
	// if err != nil {
	// 		fmt.Fprintf(os.Stderr, "GetAuthor failed: %v\n", err)
	// 		os.Exit(1)
	// }

	// fmt.Println(author.Name)
}
func closeClient() {
	logger.Infof("closing client connection")
	err := client.Stop()
	if err != nil {
		logger.Errorf("client shutdown error %v", err.Error())
	}
}

// functional
// Пополнить счёт на v рублей
func PayIn(v int64, s string) string {
	payInResp, err := sandboxService.SandboxPayIn(&investgo.SandboxPayInRequest{
		AccountId: AccId,
		Currency:  "RUB",
		Unit:      v,
		Nano:      0,
	})

	if err != nil {
		logger.Errorf(err.Error())
		return "err"
	} else {
		return fmt.Sprintf("На аккаунте песочницы %v текущий баланс = %v\n", AccId, payInResp.GetBalance().ToFloat())
	}
}
// Добавить нового пользователя в БД
func addNewUser(TGID int64, APIToken string, AccountID string) error{
	query := `
	INSERT INTO users (tgid, apitoken, accountid)
	VALUES (\$1, \$2, \$3);
	`
	_, err := conn.Exec(context.Background(), query, TGID, APIToken, AccountID)
	return err
}
func updateUser(TGID int64, APIToken string) error {

	_, err := conn.Exec(context.Background(), "UPDATE users SET apitoken=$1 WHERE tgid=$2", APIToken, TGID)
	return err
}

func listUsers() error {
	rows, _ := conn.Query(context.Background(), "SELECT * FROM users")

	for rows.Next() {
		var tgid int64
		var apitoken string
		err := rows.Scan(&tgid, &apitoken)
		if err != nil {
			return err
		}
		fmt.Printf("%d. %s\n", tgid, apitoken)
	}

	return rows.Err()
}
func findAPIByTGID(TGID int64) string{
	var apitoken string
	err := conn.QueryRow(context.Background(), "SELECT apitoken FROM users WHERE tgid=$1", TGID).Scan(&apitoken)
	switch err {
	case nil:
		return apitoken
	default:
		logger.Info(err)
	}
	return ""
}
func findIDByTGID(TGID int64) string{
	var accountid string
	err := conn.QueryRow(context.Background(), "SELECT accountid FROM users WHERE tgid=$1", TGID).Scan(&accountid)
	switch err {
	case nil:
		return accountid
	default:
		logger.Info(err)
	}
	return ""
}


func createTable() error {
	query := `
		CREATE TABLE IF NOT EXISTS users (
			id BIGINT PRIMARY KEY,
			tgid BIGINT NOT NULL,
			apitoken TEXT NOT NULL
		);`

	_, err := conn.Exec(context.Background(), query)
	if err != nil {
		return fmt.Errorf("db.ExecContext: %w", err)
	}

	return nil
}



func main() {
	loadInit()
	defer cancel()
	defer logger.Sync()
	defer closeClient()

	dbInit()
	defer conn.Close(context.Background())

	// api, _ := os.LookupEnv("TGAPI")
	bot, err := tgbotapi.NewBotAPI("6520187946:AAHAh_nDEIUG2ZBsNfjnlKSubVGxve_sGpQ")
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
			if update.Message.Command() == "start"{
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Меню")
				msg.ReplyMarkup = menuKeyboard

				bot.Send(msg)
			} else if flagAcс {
				flagAcс = false
				// add user to db
				// if user in db -> отредактировать
				TGID := update.Message.Chat.ID
				APIToken :=  update.Message.Text
				if findAPIByTGID(update.Message.Chat.ID) != ""{
					updateUser(TGID, APIToken)
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Аккаунт успешно отредактирован!")
					bot.Send(msg)
				} else {
					addNewUser(TGID, APIToken, findIDByTGID(TGID))
					listUsers()
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Аккаунт успешно зарегестрирован в боте!")
					bot.Send(msg)
				}


				
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Меню")
				msg.ReplyMarkup = menuKeyboard
				bot.Send(msg)
			} else if flagTrade {
				flagTrade = false
				s := findAPIByTGID(update.Message.Chat.ID)
				if s == "" {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Похоже, вы не добавили свой API токен в разделе 'Аккаунт'")
					bot.Send(msg)
					
					//возврат в меню
					msg = tgbotapi.NewMessage(update.Message.Chat.ID, "Меню")
					msg.ReplyMarkup = menuKeyboard
					bot.Send(msg)
				} else {
					if n, err := strconv.ParseInt(update.Message.Text, 10, 64); err == nil {
						sum := PayIn(n, findAPIByTGID(update.Message.Chat.ID))
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
				msg_edit := tgbotapi.NewEditMessageText(chatcallbackId, update.CallbackQuery.Message.MessageID, "Отправьте количество рублей, на которое желаете пополнить свой баланс")
				flagTrade = true
				if _, err := bot.Send(msg_edit); err != nil {
					panic(err)
				}
				// msg_edit := tgbotapi.NewEditMessageTextAndMarkup(chatcallbackId, update.CallbackQuery.Message.MessageID, "Аудио", audioKeyboard)
				// if _, err := bot.Send(msg_edit); err != nil {
				// 	panic(err)
				// }
			}
		}
	}
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
