// Package maxbot — тонкий слой над официальной Go-библиотекой MAX Bot API:
// ограничение частоты исходящих запросов, повторы, загрузка файлов, проверка initData.
// Весь код, зависящий от MAX, сосредоточен здесь и в пакете bot.
package maxbot

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	maxapi "github.com/max-messenger/max-bot-api-client-go/v2"
	"github.com/max-messenger/max-bot-api-client-go/v2/model"
)

// minInterval — пауза между запросами: 25 в секунду при лимите платформы 30.
const minInterval = 40 * time.Millisecond

type Client struct {
	api      *maxapi.Api
	http     *http.Client
	token    string
	mu       sync.Mutex
	last     time.Time
	BotID    int64
	Username string
	log      *slog.Logger
}

// NewHTTPClient добавляет к системным корневым сертификатам дополнительные (корневой сертификат Минцифры:
// сертификат platform-api2.max.ru выдан Russian Trusted Sub CA).
func NewHTTPClient(extraCAFile string) (*http.Client, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if extraCAFile != "" {
		pem, err := os.ReadFile(extraCAFile)
		if err != nil {
			return nil, fmt.Errorf("корневой сертификат: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("корневой сертификат %s не распознан", extraCAFile)
		}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return &http.Client{Transport: tr, Timeout: 60 * time.Second}, nil
}

func New(ctx context.Context, token string, hc *http.Client, log *slog.Logger) (*Client, error) {
	api, err := maxapi.NewApi(token, maxapi.WithHTTPClient(hc), maxapi.WithPollingTimeout(30*time.Second))
	if err != nil {
		return nil, err
	}
	c := &Client{api: api, http: hc, token: token, log: log}
	me, err := api.Bots.GetMyInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("MAX Bot API /me: %w", err)
	}
	c.BotID, c.Username = me.UserID, me.Username
	return c, nil
}

func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	next := c.last.Add(minInterval)
	now := time.Now()
	if next.Before(now) {
		next = now
	}
	c.last = next
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Until(next)):
		return nil
	}
}

// retryable — сетевые сбои и перегрузка платформы повторяем, ошибки запроса — нет.
func retryable(err error) bool {
	var ne *maxapi.NetworkError
	var te *maxapi.TimeoutError
	var ae *maxapi.Error
	switch {
	case errors.As(err, &ne), errors.As(err, &te):
		return true
	case errors.As(err, &ae):
		code := strings.ToLower(ae.Code)
		return strings.Contains(code, "too.many") || strings.Contains(code, "service") || ae.IsAttachmentNotReady()
	}
	return false
}

func (c *Client) do(ctx context.Context, op string, fn func() error) error {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if err = c.wait(ctx); err != nil {
			return err
		}
		if err = fn(); err == nil || !retryable(err) {
			break
		}
		c.log.Warn("MAX API: повтор", "op", op, "attempt", attempt+1, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(1<<attempt) * 500 * time.Millisecond):
		}
	}
	if err != nil {
		return fmt.Errorf("MAX API %s: %w", op, err)
	}
	return nil
}

// Recipient — кому отправлять: пользователю (личный чат с ботом) или в чат.
type Recipient struct {
	UserID int64
	ChatID int64
}

// Outgoing — исходящее сообщение.
type Outgoing struct {
	To       Recipient
	Text     string
	Keyboard *model.Keyboard
	// FileToken — токен ранее загруженного файла (UploadFile).
	FileToken string
	Markdown  bool
}

func (c *Client) Send(ctx context.Context, m Outgoing) (string, error) {
	msg := maxapi.NewMessage().SetText(m.Text)
	if m.To.ChatID != 0 {
		msg.SetChat(m.To.ChatID)
	} else {
		msg.SetUser(m.To.UserID)
	}
	if m.Markdown {
		msg.SetFormat(model.FormatMarkdown)
	}
	if m.FileToken != "" {
		msg.AddAttachByToken(m.FileToken, model.AttachFile)
	}
	msg.AddKeyboard(m.Keyboard)
	var res model.SendMessageResult
	err := c.do(ctx, "send", func() (err error) {
		res, err = c.api.Messages.Send(ctx, msg)
		return err
	})
	return res.Message.Body.Mid, err
}

func (c *Client) Edit(ctx context.Context, mid string, m Outgoing) error {
	body := model.NewMessageBody{Text: m.Text, Attachments: []model.Attachment{}}
	if m.Markdown {
		body.Format = model.FormatMarkdown
	}
	if m.Keyboard != nil {
		body.Attachments = append(body.Attachments, m.Keyboard.Build())
	}
	return c.do(ctx, "edit", func() error {
		_, err := c.api.Messages.EditMessage(ctx, mid, body)
		return err
	})
}

// AnswerCallback снимает «часики» с нажатой кнопки и при необходимости показывает уведомление.
func (c *Client) AnswerCallback(ctx context.Context, callbackID, notification string) error {
	ans := model.CallbackAnswer{}
	if notification != "" {
		ans.Notification = &notification
	}
	return c.do(ctx, "answer", func() error {
		_, err := c.api.Messages.AnswerOnCallback(ctx, callbackID, ans)
		return err
	})
}

// UploadFile загружает файл в MAX и возвращает токен для вложения.
func (c *Client) UploadFile(ctx context.Context, path, name string) (string, error) {
	var token string
	err := c.do(ctx, "upload", func() error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		token, err = c.api.Upload.Upload(ctx, model.UploadFile, f, name, st.Size())
		return err
	})
	return token, err
}

func (c *Client) Pin(ctx context.Context, chatID int64, mid string) error {
	return c.do(ctx, "pin", func() error {
		_, err := c.api.Chats.PinMessage(ctx, chatID, mid, false)
		return err
	})
}

// Download скачивает вложение пользователя по URL из payload.
func (c *Client) Download(ctx context.Context, url string) (io.ReadCloser, error) {
	get := func(auth bool) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		if auth {
			req.Header.Set(maxapi.AuthorizationHeader, c.token)
		}
		return c.http.Do(req)
	}
	resp, err := get(false)
	if err == nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		resp.Body.Close()
		resp, err = get(true)
	}
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("скачивание вложения: HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// Subscribe регистрирует вебхук. В проде это единственный способ получать события.
func (c *Client) Subscribe(ctx context.Context, url, secret string) error {
	types := []string{string(model.UpdateMessageCreated), string(model.UpdateMessageCallback), string(model.UpdateBotStarted), string(model.UpdateBotAdded)}
	return c.do(ctx, "subscribe", func() error {
		res, err := c.api.Subscriptions.Subscribe(ctx, url, secret, types, "")
		if err == nil && !res.Success {
			err = fmt.Errorf("подписка отклонена: %s", res.Message)
		}
		return err
	})
}

// Unsubscribe снимает все вебхуки — нужно перед long polling.
func (c *Client) UnsubscribeAll(ctx context.Context) error {
	subs, err := c.api.Subscriptions.GetSubscriptions(ctx)
	if err != nil {
		return err
	}
	for _, s := range subs.Subscriptions {
		if _, err := c.api.Subscriptions.Unsubscribe(ctx, s.URL); err != nil {
			return err
		}
	}
	return nil
}

// Handler обрабатывает одно событие.
type Handler func(ctx context.Context, u model.Update)

// WebhookHandler проверяет секрет и передаёт событие обработчику.
func (c *Client) WebhookHandler(h Handler, secret string) http.HandlerFunc {
	return c.api.GetHandler(maxapi.UpdateHandler(h), secret)
}

// Poll получает события long polling до отмены ctx (только для локальной разработки).
func (c *Client) Poll(ctx context.Context, h Handler) {
	var marker int64
	for ctx.Err() == nil {
		updates, next, err := c.api.Subscriptions.GetUpdates(ctx, marker)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.log.Warn("long polling", "err", err)
			time.Sleep(3 * time.Second)
			continue
		}
		if next > 0 {
			marker = next
		}
		for _, u := range updates {
			h(ctx, u)
		}
	}
}

// InitData — проверенные данные запуска мини-приложения.
type InitData = model.InitData

var ErrInitDataExpired = errors.New("initData устарела")

// ValidateInitData проверяет подпись (алгоритм из документации MAX, реализация официальной библиотеки)
// и срок годности auth_date.
func ValidateInitData(raw, token string, ttl time.Duration, now time.Time) (InitData, error) {
	d, err := maxapi.ValidateInitData(raw, token)
	if err != nil {
		return d, err
	}
	if ttl > 0 {
		auth := time.Unix(d.AuthDate, 0)
		if d.AuthDate > 1e12 { // на случай миллисекунд
			auth = time.UnixMilli(d.AuthDate)
		}
		if now.Sub(auth) > ttl {
			return d, ErrInitDataExpired
		}
	}
	if d.User.ID == 0 {
		return d, errors.New("initData без пользователя")
	}
	return d, nil
}
