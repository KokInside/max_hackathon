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
	"net/url"
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

// retryable — сетевые сбои, перегрузка и временная недоступность платформы повторяем, ошибки запроса — нет.
// Библиотека не сохраняет HTTP-статус, поэтому временную ошибку узнаём по коду в теле ответа, а ответ
// не в формате JSON (страница прокси при 502/503) считаем временным.
func retryable(err error) bool {
	var ne *maxapi.NetworkError
	var te *maxapi.TimeoutError
	var ae *maxapi.Error
	switch {
	case errors.As(err, &ne), errors.As(err, &te):
		return true
	case errors.As(err, &ae):
		if ae.IsAttachmentNotReady() {
			return true
		}
		code := strings.ToLower(ae.Code)
		for _, s := range []string{"too.many", "limit", "rate", "service", "unavailable", "internal", "timeout"} {
			if strings.Contains(code, s) {
				return true
			}
		}
		return false
	}
	return strings.Contains(err.Error(), "parse response error")
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
		if !retryable(err) {
			return &PermanentError{Op: op, Err: err}
		}
		return fmt.Errorf("MAX API %s: %w", op, err)
	}
	return nil
}

// PermanentError — ошибка, которую повтор не исправит (чат не найден, бот заблокирован, неверный запрос).
type PermanentError struct {
	Op  string
	Err error
}

func (e *PermanentError) Error() string   { return "MAX API " + e.Op + ": " + e.Err.Error() }
func (e *PermanentError) Unwrap() error   { return e.Err }
func (e *PermanentError) Permanent() bool { return true }

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

// maxHost — домены MAX, которым можно передать токен бота.
func maxHost(host string) bool {
	host = strings.ToLower(host)
	for _, d := range []string{"max.ru", "oneme.ru"} {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// Download скачивает вложение пользователя по URL из payload. Только HTTPS; токен бота добавляется
// при повторе после 401/403 и только для доменов MAX — чтобы не отдать его стороннему хосту.
func (c *Client) Download(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("скачивание вложения: недопустимый адрес")
	}
	get := func(auth bool) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		if auth {
			req.Header.Set(maxapi.AuthorizationHeader, c.token)
		}
		return c.http.Do(req)
	}
	resp, err := get(false)
	if err == nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && maxHost(u.Hostname()) {
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
	types := []string{string(model.UpdateMessageCreated), string(model.UpdateMessageCallback), string(model.UpdateBotStarted)}
	return c.do(ctx, "subscribe", func() error {
		res, err := c.api.Subscriptions.Subscribe(ctx, url, secret, types, "")
		if err == nil && !res.Success {
			err = fmt.Errorf("подписка отклонена: %s", res.Message)
		}
		return err
	})
}

// KeepSubscribed раз в every проверяет, что вебхук url зарегистрирован, и переподписывается при необходимости:
// MAX отписывает бота, если 8 часов не получает успешного ответа (например, после долгого сбоя сети).
func (c *Client) KeepSubscribed(ctx context.Context, url, secret string, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		subs, err := c.Subscriptions(ctx)
		if err != nil {
			c.log.Warn("MAX: проверка вебхука", "err", err)
			continue
		}
		found := false
		for _, s := range subs {
			found = found || s == url
		}
		if found {
			continue
		}
		if err := c.Subscribe(ctx, url, secret); err != nil {
			c.log.Error("MAX: переподписка на вебхук", "err", err)
			continue
		}
		c.log.Warn("MAX: вебхук был снят платформой — подписка восстановлена", "url", url)
	}
}

// Subscriptions — адреса зарегистрированных вебхуков.
func (c *Client) Subscriptions(ctx context.Context) ([]string, error) {
	subs, err := c.api.Subscriptions.GetSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(subs.Subscriptions))
	for _, s := range subs.Subscriptions {
		out = append(out, s.URL)
	}
	return out, nil
}

// UnsubscribeAll снимает все вебхуки — нужно перед long polling.
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

// WebhookHandler проверяет секрет, сразу отвечает MAX 200 и обрабатывает событие в фоне:
// долгая обработка (БД, исходящие сообщения) не должна приводить к повторной доставке.
func (c *Client) WebhookHandler(h Handler, secret string) http.HandlerFunc {
	return c.api.GetHandler(func(ctx context.Context, u model.Update) {
		go h(context.WithoutCancel(ctx), u)
	}, secret)
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
