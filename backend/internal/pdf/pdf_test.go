package pdf

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderCyrillic(t *testing.T) {
	r, err := New(filepath.Join("..", "..", "..", "assets", "fonts"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Render(Input{
		Title:     "Проверка",
		Watermark: "ДЕМО — синтетические данные",
		To:        []string{"ООО «УК Пример»"},
		From:      []string{"от председателя совета МКД"},
		Place:     "г. Демоград",
		Date:      "25.09.2026",
		Heading:   "Мотивированный отказ от подписания акта",
		Sections: []Section{{
			Heading: "Возражения по строкам акта",
			Table: &Table{
				Columns: []string{"№", "Работа", "Цена, руб.", "Возражение"},
				Widths:  []int{2, 10, 4, 8},
				Rows:    [][]string{{"6", "Очистка кровли от скопления снега и наледи", "9 600,00", "Снега в сентябре не было — работа не могла быть выполнена."}},
				Footer:  []string{"", "Итого", "9 600,00", ""},
			},
		}},
		Signature: "Председатель совета МКД",
		SignName:  "Демо Председатель",
		Footer:    "Сформировано сервисом «Приёмка»",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF")) || !bytes.Contains(out, []byte("FontFile2")) {
		t.Fatal("PDF без встроенного шрифта")
	}
	// Если в системе есть pdftotext, проверяем, что кириллица извлекается как текст.
	if _, err := exec.LookPath("pdftotext"); err == nil {
		f := filepath.Join(t.TempDir(), "out.pdf")
		if err := os.WriteFile(f, out, 0o600); err != nil {
			t.Fatal(err)
		}
		txt, err := exec.Command("pdftotext", f, "-").Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Мотивированный отказ", "Очистка кровли", "ДЕМО"} {
			if !strings.Contains(string(txt), want) {
				t.Errorf("в тексте PDF нет %q", want)
			}
		}
	}
}

// Очень длинная ячейка обрезается, а не зацикливает генерацию на пустых страницах.
func TestLongCellClipped(t *testing.T) {
	r, err := New(filepath.Join("..", "..", "..", "assets", "fonts"))
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("Работа не выполнялась, жильцы подтверждают. ", 50)
	out, err := r.Render(Input{Heading: "Тест", Sections: []Section{{Table: &Table{
		Columns: []string{"№", "Работа", "Возражение"}, Widths: []int{2, 10, 12},
		Rows: [][]string{{"1", "Уборка", long}},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(out, []byte("/Type /Page\n")) + bytes.Count(out, []byte("/Type /Page ")); n > 2 {
		t.Fatalf("страниц %d — длинная ячейка не обрезана", n)
	}
}
