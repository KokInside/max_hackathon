package files

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

var png = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde")

func TestSave(t *testing.T) {
	s, err := New(t.TempDir(), []byte("secret-secret-secret"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Save(bytes.NewReader(png), 1<<20, ImagesAndPDF)
	if err != nil || got.Mime != "image/png" || !strings.HasSuffix(got.Key, ".png") || got.Size != int64(len(png)) {
		t.Fatalf("%+v %v", got, err)
	}
	// Тип определяется по содержимому: текст с «расширением» PDF не пройдёт.
	if _, err := s.Save(strings.NewReader("hello, not a pdf"), 1<<20, ImagesAndPDF); !errors.Is(err, ErrBadType) {
		t.Fatalf("текст принят: %v", err)
	}
	if _, err := s.Save(bytes.NewReader(nil), 1<<20, ImagesAndPDF); !errors.Is(err, ErrBadType) {
		t.Fatalf("пустой файл принят: %v", err)
	}
	big := append(append([]byte{}, png...), make([]byte, 2048)...)
	if _, err := s.Save(bytes.NewReader(big), 1024, ImagesAndPDF); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("лимит размера не сработал: %v", err)
	}
	pdf := []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	if got, err := s.Save(bytes.NewReader(pdf), 1<<20, ImagesAndPDF); err != nil || got.Mime != "application/pdf" {
		t.Fatalf("PDF: %+v %v", got, err)
	}
	if err := s.Remove(got.Key); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(got.Key); err != nil {
		t.Fatal("повторное удаление не должно быть ошибкой")
	}
}

func TestSignVerify(t *testing.T) {
	s, _ := New(t.TempDir(), []byte("secret-secret-secret"))
	exp, sig := s.Sign("file-1", time.Minute)
	e := strconv.FormatInt(exp, 10)
	if err := s.Verify("file-1", e, sig); err != nil {
		t.Fatal(err)
	}
	if s.Verify("file-2", e, sig) == nil {
		t.Fatal("подпись подошла к другому файлу")
	}
	if s.Verify("file-1", strconv.FormatInt(exp+3600, 10), sig) == nil {
		t.Fatal("продлённый срок принят")
	}
	exp, sig = s.Sign("file-1", -time.Second)
	if s.Verify("file-1", strconv.FormatInt(exp, 10), sig) == nil {
		t.Fatal("просроченная ссылка принята")
	}
	other, _ := New(t.TempDir(), []byte("another-secret-value"))
	exp, sig = other.Sign("file-1", time.Minute)
	if s.Verify("file-1", strconv.FormatInt(exp, 10), sig) == nil {
		t.Fatal("подпись чужим ключом принята")
	}
	if tok := RandomToken(16); len(tok) != 16 || strings.Trim(tok, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
		t.Fatalf("токен %q", tok)
	}
}
