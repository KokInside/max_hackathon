// Package pdf верстает документы (письмо, мотивированный отказ, демо-акт) из подготовленных строк.
// Тексты формирует app по шаблонам config/templates; здесь только раскладка на странице.
// Кириллица: стандартные шрифты PDF её не содержат, поэтому встраивается PT Serif (SIL OFL 1.1).
package pdf

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/border"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/core/entity"
	"github.com/johnfercher/maroto/v2/pkg/fontrepository"
	"github.com/johnfercher/maroto/v2/pkg/props"
)

const (
	family = "ptserif"
	grid   = 24
)

type Renderer struct {
	fonts []entity.CustomFont
}

// New загружает шрифты из каталога assets/fonts.
func New(fontsDir string) (*Renderer, error) {
	fonts, err := fontrepository.New().
		AddUTF8Font(family, fontstyle.Normal, filepath.Join(fontsDir, "PT_Serif-Web-Regular.ttf")).
		AddUTF8Font(family, fontstyle.Bold, filepath.Join(fontsDir, "PT_Serif-Web-Bold.ttf")).
		AddUTF8Font(family, fontstyle.Italic, filepath.Join(fontsDir, "PT_Serif-Web-Italic.ttf")).
		AddUTF8Font(family, fontstyle.BoldItalic, filepath.Join(fontsDir, "PT_Serif-Web-Bold.ttf")).
		Load()
	if err != nil {
		return nil, fmt.Errorf("шрифты PDF: %w", err)
	}
	return &Renderer{fonts: fonts}, nil
}

// Table — таблица с шириной колонок в долях сетки из 24.
type Table struct {
	Columns []string
	Widths  []int
	Rows    [][]string
	// Footer — итоговая строка (необязательно).
	Footer []string
}

// Section — раздел документа.
type Section struct {
	Heading    string
	Paragraphs []string
	Table      *Table
	Bullets    []string
}

// Input — документ целиком, все строки уже готовы.
type Input struct {
	Title     string
	Watermark string
	// Header — реквизиты справа вверху: кому и от кого.
	To   []string
	From []string
	// Place и Date — строка «г. …, дата».
	Place      string
	Date       string
	Heading    string
	Subheading string
	Paragraphs []string
	Sections   []Section
	Closing    []string
	Signature  string
	SignName   string
	Footer     string
	// Created — дата создания в свойствах PDF.
	Created time.Time
}

var (
	gray   = &props.Color{Red: 110, Green: 110, Blue: 110}
	red    = &props.Color{Red: 190, Green: 30, Blue: 30}
	light  = &props.Color{Red: 235, Green: 235, Blue: 235}
	cell   = &props.Cell{BorderType: border.Full, BorderThickness: 0.2, BorderColor: &props.Color{Red: 80, Green: 80, Blue: 80}}
	header = &props.Cell{BorderType: border.Full, BorderThickness: 0.2, BorderColor: &props.Color{Red: 80, Green: 80, Blue: 80}, BackgroundColor: light}
	body   = props.Text{Size: 11, Align: align.Left, Top: 1, Bottom: 1}
)

// Render формирует PDF.
func (r *Renderer) Render(in Input) ([]byte, error) {
	created := in.Created
	if created.IsZero() {
		created = time.Now()
	}
	cfg := config.NewBuilder().
		WithPageSize(pagesize.A4).
		WithMaxGridSize(grid).
		WithLeftMargin(25).WithRightMargin(15).WithTopMargin(15).WithBottomMargin(15).
		WithCustomFonts(r.fonts).
		WithDefaultFont(&props.Font{Family: family, Size: 11}).
		WithPageNumber(props.PageNumber{Pattern: "Стр. {current} из {total}", Place: props.RightBottom, Family: family, Size: 8, Color: gray}).
		WithTitle(in.Title, true).
		WithCreator("Приёмка", true).
		WithCreationDate(created).
		Build()
	m := maroto.New(cfg)

	if in.Watermark != "" {
		if err := m.RegisterHeader(text.NewRow(7, in.Watermark, props.Text{Size: 10, Style: fontstyle.Bold, Align: align.Center, Color: red})); err != nil {
			return nil, err
		}
	}
	if in.Footer != "" {
		if err := m.RegisterFooter(text.NewRow(6, in.Footer, props.Text{Size: 7, Color: gray, Top: 1})); err != nil {
			return nil, err
		}
	}

	addBlock(m, in.To, 11)
	if len(in.To) > 0 {
		m.AddRows(row.New(3))
	}
	addBlock(m, in.From, 11)
	if in.Place != "" || in.Date != "" {
		m.AddRow(8,
			text.NewCol(12, in.Place, props.Text{Top: 3}),
			text.NewCol(12, in.Date, props.Text{Top: 3, Align: align.Right}),
		)
	}
	if in.Heading != "" {
		m.AddRows(row.New(4))
		m.AddAutoRow(text.NewCol(grid, in.Heading, props.Text{Size: 13, Style: fontstyle.Bold, Align: align.Center}))
	}
	if in.Subheading != "" {
		m.AddAutoRow(text.NewCol(grid, in.Subheading, props.Text{Size: 11, Align: align.Center, Top: 1}))
	}
	m.AddRows(row.New(3))
	for _, p := range in.Paragraphs {
		paragraph(m, p)
	}
	for _, s := range in.Sections {
		section(m, s)
	}
	for _, p := range in.Closing {
		paragraph(m, p)
	}
	if in.Signature != "" {
		m.AddRows(row.New(8))
		m.AddRow(7,
			text.NewCol(11, in.Signature),
			col.New(6).Add(line.New(props.Line{OffsetPercent: 90, Thickness: 0.2})),
			text.NewCol(7, in.SignName, props.Text{Align: align.Right}),
		)
		m.AddRow(5,
			col.New(11),
			text.NewCol(6, "(подпись)", props.Text{Size: 7, Align: align.Center, Color: gray}),
			col.New(7),
		)
	}

	doc, err := m.Generate()
	if err != nil {
		return nil, err
	}
	return doc.GetBytes(), nil
}

func addBlock(m core.Maroto, lines []string, offset int) {
	for _, l := range lines {
		m.AddAutoRow(col.New(offset), text.NewCol(grid-offset, l, props.Text{Size: 11}))
	}
}

func paragraph(m core.Maroto, p string) {
	m.AddAutoRow(text.NewCol(grid, p, body))
	m.AddRows(row.New(1.5))
}

func section(m core.Maroto, s Section) {
	if s.Heading != "" {
		m.AddRows(row.New(3))
		m.AddAutoRow(text.NewCol(grid, s.Heading, props.Text{Style: fontstyle.Bold, Size: 11.5, Bottom: 1}))
	}
	if s.Table != nil {
		table(m, *s.Table)
		m.AddRows(row.New(2))
	}
	for _, p := range s.Paragraphs {
		paragraph(m, p)
	}
	for _, b := range s.Bullets {
		m.AddAutoRow(text.NewCol(1, "—", props.Text{Align: align.Right, Right: 2, Top: 1}), text.NewCol(grid-1, b, body))
	}
}

// maxCell — страховочный предел текста ячейки: строку таблицы выше страницы maroto не переносит
// и зацикливается на пустых страницах. Длинные тексты вызывающий код выносит из таблицы сам.
const maxCell = 600

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func table(m core.Maroto, t Table) {
	hdr := make([]core.Col, len(t.Columns))
	for i, c := range t.Columns {
		hdr[i] = text.NewCol(t.Widths[i], c, props.Text{Size: 8, Style: fontstyle.Bold, Align: align.Center, Top: 1, Bottom: 1, Left: 1, Right: 1}).WithStyle(header)
	}
	m.AddAutoRow(hdr...)
	add := func(vals []string, style fontstyle.Type) {
		cols := make([]core.Col, len(t.Columns))
		for i := range t.Columns {
			v := ""
			if i < len(vals) {
				v = clip(vals[i], maxCell)
			}
			cols[i] = text.NewCol(t.Widths[i], v, props.Text{Size: 8.5, Style: style, Top: 1, Bottom: 1, Left: 1, Right: 1}).WithStyle(cell)
		}
		m.AddAutoRow(cols...)
	}
	for _, r := range t.Rows {
		add(r, fontstyle.Normal)
	}
	if len(t.Footer) > 0 {
		add(t.Footer, fontstyle.Bold)
	}
}
