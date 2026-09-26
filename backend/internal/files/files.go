// Package files хранит загруженные и сформированные файлы на диске (docker-том)
// и выдаёт на них подписанные ссылки с ограниченным сроком действия.
package files

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

var (
	ErrTooLarge     = errors.New("файл слишком большой")
	ErrBadType      = errors.New("неподдерживаемый тип файла")
	ErrBadSignature = errors.New("ссылка недействительна или устарела")
)

// ImagesAndPDF — разрешённые типы загрузок: фото и PDF (тип определяется по содержимому, не по имени).
var ImagesAndPDF = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "application/pdf": ".pdf"}

type Storage struct {
	dir    string
	secret []byte
}

func New(dir string, secret []byte) (*Storage, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &Storage{dir: dir, secret: secret}, nil
}

type Saved struct {
	Key    string
	Mime   string
	Size   int64
	SHA256 string
}

// Save читает не больше maxSize байт, определяет тип по содержимому и сохраняет файл.
func (s *Storage) Save(r io.Reader, maxSize int64, allowed map[string]string) (Saved, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return Saved{}, err
	}
	head = head[:n]
	mime := http.DetectContentType(head)
	if i := bytes.IndexByte([]byte(mime), ';'); i >= 0 {
		mime = mime[:i]
	}
	ext, ok := allowed[mime]
	if !ok {
		return Saved{}, fmt.Errorf("%w: %s", ErrBadType, mime)
	}

	key := time.Now().UTC().Format("2006/01/") + randomHex(16) + ext
	path := filepath.Join(s.dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return Saved{}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return Saved{}, err
	}
	h := sha256.New()
	w := io.MultiWriter(f, h)
	size, err := io.Copy(w, io.MultiReader(bytes.NewReader(head), io.LimitReader(r, maxSize+1-int64(len(head)))))
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil && size > maxSize {
		err = ErrTooLarge
	}
	if err != nil {
		_ = os.Remove(path)
		return Saved{}, err
	}
	return Saved{Key: key, Mime: mime, Size: size, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// SaveBytes сохраняет сформированный сервисом файл (PDF).
func (s *Storage) SaveBytes(b []byte, ext string) (Saved, error) {
	key := time.Now().UTC().Format("2006/01/") + randomHex(16) + ext
	path := filepath.Join(s.dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return Saved{}, err
	}
	if err := os.WriteFile(path, b, 0o640); err != nil {
		return Saved{}, err
	}
	sum := sha256.Sum256(b)
	return Saved{Key: key, Mime: http.DetectContentType(b), Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])}, nil
}

func (s *Storage) Open(key string) (*os.File, error) {
	return os.Open(filepath.Join(s.dir, filepath.FromSlash(key)))
}

// Remove удаляет файл с диска; отсутствие файла ошибкой не считается.
func (s *Storage) Remove(key string) error {
	err := os.Remove(filepath.Join(s.dir, filepath.FromSlash(key)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Storage) Path(key string) string { return filepath.Join(s.dir, filepath.FromSlash(key)) }

// Sign возвращает параметры подписанной ссылки на файл.
func (s *Storage) Sign(fileID string, ttl time.Duration) (exp int64, sig string) {
	exp = time.Now().Add(ttl).Unix()
	return exp, s.mac(fileID, exp)
}

func (s *Storage) Verify(fileID, expStr, sig string) error {
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return ErrBadSignature
	}
	if !hmac.Equal([]byte(sig), []byte(s.mac(fileID, exp))) {
		return ErrBadSignature
	}
	return nil
}

func (s *Storage) mac(fileID string, exp int64) string {
	m := hmac.New(sha256.New, s.secret)
	fmt.Fprintf(m, "%s|%d", fileID, exp)
	return hex.EncodeToString(m.Sum(nil))[:32]
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// RandomToken — токен для приглашений: [A-Za-z0-9_-], подходит для start_param.
func RandomToken(n int) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
