package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mdp/qrterminal/v3"
	"github.com/sirupsen/logrus"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waTypes "go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	_ "github.com/mattn/go-sqlite3"
)

type Scheduler struct {
	tasks  map[string]*ScheduledTask
	mutex  sync.RWMutex
	client *whatsmeow.Client
}

type ScheduledTask struct {
	ID          string    `json:"id"`
	ChatName    string    `json:"chat_name"`
	Message     string    `json:"message"`
	Interval    int       `json:"interval"`
	RandomDelay int       `json:"random_delay"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	stopChan    chan bool
}

// UnmarshalJSON для правильного парсинга времени
func (t *ScheduledTask) UnmarshalJSON(data []byte) error {
	type Alias ScheduledTask
	aux := &struct {
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
		*Alias
	}{
		Alias: (*Alias)(t),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Парсим время в формате "2006-01-02T15:04:05.000Z"
	if aux.StartTime != "" {
		startTime, err := time.Parse("2006-01-02T15:04:05.000Z", aux.StartTime)
		if err != nil {
			return fmt.Errorf("неверный формат времени начала: %v", err)
		}
		t.StartTime = startTime
	}

	if aux.EndTime != "" {
		endTime, err := time.Parse("2006-01-02T15:04:05.000Z", aux.EndTime)
		if err != nil {
			return fmt.Errorf("неверный формат времени окончания: %v", err)
		}
		t.EndTime = endTime
	}

	return nil
}

type MessageRequest struct {
	ChatName    string `json:"chat_name"`
	Message     string `json:"message"`
	Interval    int    `json:"interval"`
	RandomDelay int    `json:"random_delay"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
}

var (
	scheduler *Scheduler
	logger    = logrus.New()
)

// openBrowser открывает браузер с указанным URL
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default: // linux, freebsd, openbsd, netbsd
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			if scheduler != nil && scheduler.client != nil {
				// Отключаем клиента WhatsApp при панике
				scheduler.client.Disconnect()
			}
			logger.Errorf("Программа завершилась с ошибкой: %v", r)
			logger.Info("Нажмите любую Enter для выхода...")
			fmt.Scanln()
			os.Exit(-1)
		}
	}()

	// Инициализация логгера
	logger.SetLevel(logrus.InfoLevel)
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	// Инициализация планировщика
	scheduler = &Scheduler{
		tasks: make(map[string]*ScheduledTask),
		mutex: sync.RWMutex{},
	}

	// Инициализация WhatsApp клиента
	if err := initWhatsApp(); err != nil {
		logger.Fatal("Ошибка инициализации WhatsApp:", err)
	}

	// Настройка Gin
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// Настройка логирования Gin
	r.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		// Логируем только ошибки и важные запросы, исключаем /tasks
		if param.StatusCode >= 400 || (param.Path != "/tasks" && param.Method != "GET") {
			return fmt.Sprintf("[GIN] %v | %3d | %13v | %15s | %-7s %s\n",
				param.TimeStamp.Format("2006/01/02 - 15:04:05"),
				param.StatusCode,
				param.Latency,
				param.ClientIP,
				param.Method,
				param.Path,
			)
		}
		return ""
	}))

	// Загрузка HTML шаблонов
	r.LoadHTMLGlob("ui_templates/*")
	//r.Static("/static", "./static")

	// Маршруты
	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", gin.H{
			"title": "WhatsApp Scheduler",
		})
	})

	r.GET("/qr", func(c *gin.Context) {
		if scheduler.client == nil || scheduler.client.Store.ID == nil {
			c.JSON(http.StatusOK, gin.H{"qr": "Клиент не инициализирован", "authorized": false})
			return
		}

		// Проверяем статус подключения
		connected := scheduler.client.IsConnected()
		if connected {
			c.JSON(http.StatusOK, gin.H{"qr": "QR код уже отсканирован", "authorized": true, "connected": true})
		} else {
			c.JSON(http.StatusOK, gin.H{"qr": "QR код отсканирован, но соединение потеряно", "authorized": true, "connected": false})
		}
	})

	r.GET("/status", func(c *gin.Context) {
		if scheduler.client == nil {
			c.JSON(http.StatusOK, gin.H{
				"initialized": false,
				"authorized":  false,
				"connected":   false,
				"message":     "Клиент не инициализирован",
			})
			return
		}

		authorized := scheduler.client.Store.ID != nil
		connected := scheduler.client.IsConnected()

		status := gin.H{
			"initialized": true,
			"authorized":  authorized,
			"connected":   connected,
		}

		if !authorized {
			status["message"] = "Требуется авторизация через QR код"
		} else if !connected {
			status["message"] = "Соединение потеряно, требуется переподключение"
		} else {
			status["message"] = "Готов к работе"
		}

		c.JSON(http.StatusOK, status)
	})

	r.POST("/schedule", func(c *gin.Context) {
		var task ScheduledTask
		if err := c.ShouldBindJSON(&task); err != nil {
			fmt.Printf("schedule error: %s\n", err.Error())
			c.JSON(400, gin.H{"error": "Ошибка парсинга JSON: " + err.Error()})
			return
		}

		// Проверяем, есть ли уже активная задача
		existingTask := scheduler.GetCurrentTask()
		if existingTask != nil {
			c.JSON(409, gin.H{
				"error":         "Уже есть активная задача",
				"existing_task": existingTask,
				"message":       "Хотите заменить существующую задачу?",
			})
			return
		}

		// Создаем задачу
		newTask := &ScheduledTask{
			ChatName:    strings.TrimSpace(task.ChatName),
			Message:     strings.TrimSpace(task.Message),
			Interval:    task.Interval,
			RandomDelay: task.RandomDelay,
			StartTime:   task.StartTime,
			EndTime:     task.EndTime,
			stopChan:    make(chan bool),
		}

		taskID, err := scheduler.AddTask(newTask)
		if err == nil {
			c.JSON(200, gin.H{"message": "Задача добавлена", "task_id": taskID})
		} else {
			c.JSON(400, gin.H{"error": "Ошибка при добавлении задачи: " + err.Error()})
		}
	})

	r.GET("/tasks", func(c *gin.Context) {
		scheduler.mutex.RLock()
		defer scheduler.mutex.RUnlock()

		tasks := make([]*ScheduledTask, 0, len(scheduler.tasks))
		for _, task := range scheduler.tasks {
			tasks = append(tasks, task)
		}
		c.JSON(http.StatusOK, tasks)
	})

	r.POST("/stop/:id", func(c *gin.Context) {
		id := c.Param("id")
		if scheduler.StopTask(id) {
			c.JSON(http.StatusOK, gin.H{"message": "Задача остановлена"})
		} else {
			c.JSON(http.StatusNotFound, gin.H{"error": "Задача не найдена"})
		}
	})

	r.POST("/test", func(c *gin.Context) {
		var req struct {
			ChatName string `json:"chat_name"`
			Message  string `json:"message"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if err := scheduler.SendTestMessage(req.ChatName, req.Message); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"error":   err.Error(),
				"message": "Ошибка отправки тестового сообщения",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "Тестовое сообщение успешно отправлено",
			"chat":    req.ChatName,
			"text":    req.Message,
		})
	})

	// Endpoint для замены существующей задачи
	r.POST("/replace-task", func(c *gin.Context) {
		var task ScheduledTask
		if err := c.ShouldBindJSON(&task); err != nil {
			c.JSON(400, gin.H{"error": "Ошибка парсинга JSON: " + err.Error()})
			return
		}

		task.StartTime = task.StartTime.In(time.Local)
		task.EndTime = task.EndTime.In(time.Local)

		// Создаем задачу
		newTask := &ScheduledTask{
			ChatName:    strings.TrimSpace(task.ChatName),
			Message:     strings.TrimSpace(task.Message),
			Interval:    task.Interval,
			RandomDelay: task.RandomDelay,
			StartTime:   task.StartTime,
			EndTime:     task.EndTime,
			stopChan:    make(chan bool),
		}

		taskID, err := scheduler.AddTask(newTask)
		if err == nil {
			c.JSON(200, gin.H{"message": "Задача заменена", "task_id": taskID})
		} else {
			c.JSON(400, gin.H{"error": "Ошибка при замене задачи: " + err.Error()})
		}
	})

	// Запускаем сервер в горутине
	go func() {
		logger.Info("Сервер запущен на http://localhost:8080")
		if err := r.Run(":8080"); err != nil {
			logger.Fatal("Ошибка запуска сервера:", err)
		}
	}()

	// Ждем немного для запуска сервера
	time.Sleep(2 * time.Second)

	// Открываем браузер
	logger.Info("Открываем браузер... | UI: http://localhost:8080")
	if err := openBrowser("http://localhost:8080"); err != nil {
		logger.Warn("Не удалось открыть браузер автоматически:", err)
		logger.Info("Пожалуйста, откройте браузер и перейдите по адресу: http://localhost:8080")
	}

	// Ждем завершения программы
	select {}
}

func initWhatsApp() error {
	// Создаем базу данных с поддержкой foreign keys
	db, err := sql.Open("sqlite3", "whatsmeow.db?_foreign_keys=on")
	if err != nil {
		return fmt.Errorf("ошибка открытия БД: %v", err)
	}
	defer db.Close()

	// Включаем foreign keys
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	if err != nil {
		return fmt.Errorf("ошибка включения foreign keys: %v", err)
	}

	container, err := sqlstore.New(context.Background(), "sqlite3", "whatsmeow.db?_foreign_keys=on", nil)
	if err != nil {
		return fmt.Errorf("ошибка создания контейнера: %v", err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		return fmt.Errorf("ошибка получения устройства: %v", err)
	}

	client := whatsmeow.NewClient(deviceStore, nil)

	// Упрощенный обработчик событий - только для логирования ошибок
	client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Receipt:
			// Логируем только подтверждения отправки
			if v.Type == events.ReceiptTypeDelivered || v.Type == events.ReceiptTypeRead {
				logger.Debugf("Сообщение доставлено/прочитано: %s", v.MessageIDs)
			}
		case *events.Connected:
			logger.Info("✅ Подключение к WhatsApp установлено")
		case *events.Disconnected:
			logger.Warn("⚠️ Отключение от WhatsApp")
		}
	})

	if client.Store.ID == nil {
		logger.Info("Клиент не авторизован. Сканируйте QR код: | UI: http://localhost:8080")
		qrChan, _ := client.GetQRChannel(context.Background())
		err = client.Connect()
		if err != nil {
			return fmt.Errorf("ошибка подключения: %v", err)
		}
		for evt := range qrChan {
			if evt.Event == "code" {
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			} else {
				logger.Info("QR код отсканирован! Авторизация завершена. | UI: http://localhost:8080")
				break
			}
		}
	} else {
		err = client.Connect()
		if err != nil {
			return fmt.Errorf("ошибка подключения: %v", err)
		}
		logger.Info("WhatsApp клиент подключен | UI: http://localhost:8080")
	}

	// Проверяем статус подключения
	if !client.IsConnected() {
		return fmt.Errorf("не удалось установить подключение к WhatsApp")
	}

	scheduler.client = client
	return nil
}

// GetCurrentTask возвращает текущую активную задачу (если есть)
func (s *Scheduler) GetCurrentTask() *ScheduledTask {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Возвращаем первую найденную задачу (у нас только одна)
	for _, task := range s.tasks {
		return task
	}
	return nil
}

// AddTask добавляет новую задачу, заменяя существующую если нужно
func (s *Scheduler) AddTask(task *ScheduledTask) (string, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Проверяем, есть ли уже активная задача
	var existingTask *ScheduledTask
	for _, t := range s.tasks {
		existingTask = t
		break
	}

	if existingTask != nil {
		// Останавливаем существующую задачу
		close(existingTask.stopChan)
		delete(s.tasks, existingTask.ID)
		logger.Infof("🔄 Остановлена существующая задача %s для замены новой | UI: http://localhost:8080", existingTask.ID)
	}

	// Проверяем валидность данных
	if task.ChatName == "" {
		return "", fmt.Errorf("пустое название чата")
	}
	if task.Message == "" {
		return "", fmt.Errorf("пустое сообщение")
	}
	if task.Interval <= 0 {
		return "", fmt.Errorf("неверный интервал: %d", task.Interval)
	}
	if task.StartTime.IsZero() {
		return "", fmt.Errorf("неверное время начала")
	}
	if task.EndTime.IsZero() {
		return "", fmt.Errorf("неверное время окончания")
	}

	// Добавляем новую задачу
	task.ID = fmt.Sprintf("task_%d", time.Now().UnixNano())
	task.stopChan = make(chan bool)
	s.tasks[task.ID] = task

	logger.Infof("🚀 Добавлена задача %s для чата '%s' (интервал: %d мин, задержка: %d мин) | UI: http://localhost:8080",
		task.ID, task.ChatName, task.Interval, task.RandomDelay)

	// Запускаем задачу в горутине
	go s.runTask(task)

	return task.ID, nil
}

func (s *Scheduler) StopTask(id string) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	task, exists := s.tasks[id]
	if !exists {
		logger.Warnf("Попытка остановить несуществующую задачу: %s | UI: http://localhost:8080", id)
		return false
	}

	close(task.stopChan)
	delete(s.tasks, id)
	logger.Infof("⏹️ Задача %s остановлена (чат: %s) | UI: http://localhost:8080", id, task.ChatName)
	return true
}

func (s *Scheduler) runTask(task *ScheduledTask) {
	logger.Infof("🔄 Запуск планировщика для задачи %s (чат: %s) | UI: http://localhost:8080", task.ID, task.ChatName)

	if time.Now().After(task.EndTime) {
		logger.Infof("⏰ Задача %s уже завершена по времени до первой отправки (чат: %s) | UI: http://localhost:8080", task.ID, task.ChatName)
		s.mutex.Lock()
		delete(s.tasks, task.ID)
		s.mutex.Unlock()
		return
	}

	if task.Interval <= 0 {
		logger.Errorf("❌ Неверный интервал для задачи %s(не может быть меньше 1 минуты) (чат: %s) | UI: http://localhost:8080", task.ID, task.ChatName)
		s.mutex.Lock()
		delete(s.tasks, task.ID)
		s.mutex.Unlock()
		return
	}

	if task.RandomDelay > task.Interval {
		logger.Errorf("❌ Случайная задержка должна быть меньше интервала для задачи %s (чат: %s) | UI: http://localhost:8080", task.ID, task.ChatName)
		s.mutex.Lock()
		delete(s.tasks, task.ID)
		s.mutex.Unlock()
		return
	}

	// Функция отправки сообщения с случайной задержкой
	sendMessageWithDelay := func() {
		// Добавляем случайную задержку
		randomDelayMinutes := rand.Intn(task.RandomDelay + 1) // от 0 до RandomDelay включительно
		if randomDelayMinutes > 0 {
			randomDelay := time.Duration(randomDelayMinutes) * time.Minute
			logger.Infof("🎲 Случайная задержка для задачи %s: %d минут | UI: http://localhost:8080", task.ID, randomDelayMinutes)

			select {
			case <-task.stopChan:
				return
			case <-time.After(randomDelay):
				// Задержка завершена, проверяем время еще раз
				if time.Now().After(task.EndTime) {
					logger.Infof("⏰ Задача %s завершена по времени во время случайной задержки (чат: %s) | UI: http://localhost:8080", task.ID, task.ChatName)
					s.mutex.Lock()
					delete(s.tasks, task.ID)
					s.mutex.Unlock()
					return
				}
			}
		}

		logger.Infof("📤 Отправка сообщения по задаче %s в чат '%s' | UI: http://localhost:8080", task.ID, task.ChatName)
		if err := s.sendMessage(task.ChatName, task.Message); err != nil {
			logger.Errorf("❌ Ошибка отправки сообщения по задаче %s: %v | UI: http://localhost:8080", task.ID, err)
		} else {
			logger.Infof("✅ Сообщение по задаче %s отправлено успешно | UI: http://localhost:8080", task.ID)
		}
	}

	// Проверяем корректность сравнения времени начала задачи и текущего времени.
	now := time.Now()
	nextSendTime := task.StartTime

	// Если время начала в прошлом, вычисляем следующее время отправки
	if nextSendTime.Before(now) {
		intervalDuration := time.Duration(task.Interval) * time.Minute
		// Вычисляем сколько интервалов прошло с момента startTime
		timePassed := now.Sub(task.StartTime)
		intervalsPassed := int(timePassed / intervalDuration)

		// Следующее время отправки = startTime + (intervalsPassed + 1) * interval
		nextSendTime = task.StartTime.Add(time.Duration(intervalsPassed+1) * intervalDuration)

		logger.Infof("⏰ Время начала в прошлом. Следующая отправка запланирована на: %s | UI: http://localhost:8080",
			nextSendTime.Format("15:04:05 02.01.2006"))
	}

	defer func() {
		s.mutex.Lock()
		delete(s.tasks, task.ID)
		s.mutex.Unlock()
	}()

	// Основной цикл для повторных отправок
	for {
		if nextSendTime.After(task.EndTime) {
			logger.Infof("⏰ Задача %s завершена по времени (чат: %s) | UI: http://localhost:8080", task.ID, task.ChatName)
			return
		}

		// Логируем время до следующей отправки
		timeUntilSend := nextSendTime.Sub(now)
		if timeUntilSend > 0 {
			minutesUntilSend := int(timeUntilSend.Minutes())
			if minutesUntilSend > 0 {
				logger.Infof("⏳ До отправки сообщения: %d минут (%s) | UI: http://localhost:8080",
					minutesUntilSend, nextSendTime.Format("15:04:05 02.01.2006"))
			} else {
				logger.Infof("⏳ До отправки сообщения: %d секунд | UI: http://localhost:8080",
					int(timeUntilSend.Seconds()))
			}
		}

		select {
		case <-task.stopChan:
			logger.Infof("🛑 Планировщик остановлен для задачи %s | UI: http://localhost:8080", task.ID)
			return
		case <-time.After(timeUntilSend):
			sendMessageWithDelay()
			nextSendTime = nextSendTime.Add(time.Duration(task.Interval) * time.Minute)
		}
	}
}

func (s *Scheduler) sendMessage(chatName, message string) error {
	if s.client == nil {
		return fmt.Errorf("клиент не инициализирован")
	}

	// Проверяем подключение
	if !s.client.IsConnected() {
		logger.Warnf("Клиент не подключен, пытаемся переподключиться...")
		if err := s.client.Connect(); err != nil {
			return fmt.Errorf("не удалось переподключиться к WhatsApp: %v", err)
		}
	}

	// Очищаем входные данные
	chatName = strings.TrimSpace(chatName)
	message = strings.TrimSpace(message)

	if chatName == "" {
		return fmt.Errorf("название чата не может быть пустым")
	}

	if message == "" {
		return fmt.Errorf("сообщение не может быть пустым")
	}

	logger.Infof("Попытка отправки сообщения в чат '%s': %s", chatName, message)

	var targetJID waTypes.JID

	// Сначала пробуем создать JID из введенного текста (только если это похоже на номер телефона)
	if len(chatName) >= 10 && len(chatName) <= 15 && strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, chatName) == chatName {
		// Только если это чисто цифры (номер телефона)
		targetJID = waTypes.NewJID(chatName, "s.whatsapp.net")
		logger.Debugf("Создан JID из номера: %s", targetJID)
	}

	// Если JID не создан, ищем в контактах
	if targetJID.IsEmpty() {
		logger.Debugf("JID не создан, ищем в контактах: %s", chatName)
		contacts, err := s.client.Store.Contacts.GetAllContacts(context.Background())
		if err != nil {
			return fmt.Errorf("ошибка получения контактов: %v", err)
		}

		// Ищем контакт по имени
		for jid, contact := range contacts {
			if contact.FullName != "" && contact.FullName == chatName {
				targetJID = jid
				logger.Debugf("Найден контакт по имени '%s': %s", contact.FullName, jid)
				break
			}
		}

		// Если контакт не найден по имени, пробуем найти по номеру телефона
		if targetJID.IsEmpty() {
			for jid := range contacts {
				if jid.String() == chatName {
					targetJID = jid
					logger.Debugf("Найден контакт по номеру: %s", jid)
					break
				}
			}
		}
	}

	if targetJID.IsEmpty() {
		return fmt.Errorf("чат '%s' не найден. Убедитесь, что указали правильное имя чата или номер телефона", chatName)
	}

	logger.Infof("Отправляем сообщение в %s (%s)", chatName, targetJID)

	// Создаем сообщение
	msg := &waE2E.Message{
		Conversation: proto.String(message),
	}

	// Отправляем сообщение с таймаутом
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := s.client.SendMessage(ctx, targetJID, msg)
	if err != nil {
		logger.Errorf("Ошибка отправки сообщения в %s: %v", targetJID, err)

		// Проверяем тип ошибки
		if strings.Contains(err.Error(), "timed out") {
			return fmt.Errorf("таймаут отправки сообщения. Проверьте подключение к интернету и попробуйте снова")
		} else if strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("чат '%s' не найден или недоступен", chatName)
		} else if strings.Contains(err.Error(), "unauthorized") {
			return fmt.Errorf("не авторизован в WhatsApp. Пожалуйста, отсканируйте QR код заново")
		}

		return fmt.Errorf("ошибка отправки сообщения: %v", err)
	}

	logger.Infof("✅ Сообщение успешно отправлено в чат '%s' (%s): %s | UI: http://localhost:8080", chatName, targetJID, message)
	return nil
}

func (s *Scheduler) SendTestMessage(chatName, message string) error {
	logger.Infof("🧪 Отправка тестового сообщения в чат '%s' | UI: http://localhost:8080", chatName)
	return s.sendMessage(chatName, message)
}
