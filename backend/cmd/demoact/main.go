// demoact формирует синтетический акт по форме приказа Минстроя № 761/пр (demo/act-demo.pdf).
// Его можно прислать боту как «полученный от УК акт» при проверке решения.
//
//	go run ./cmd/demoact -config ../config -fonts ../assets/fonts -out ../demo/act-demo.pdf
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"priemka/internal/app"
	"priemka/internal/civil"
	"priemka/internal/domain"
	"priemka/internal/pdf"
)

func main() {
	cfg := flag.String("config", "../config", "каталог config")
	fonts := flag.String("fonts", "../assets/fonts", "каталог шрифтов")
	out := flag.String("out", "../demo/act-demo.pdf", "куда сохранить")
	flag.Parse()

	d, err := app.LoadDemo(*cfg)
	if err != nil {
		log.Fatal(err)
	}
	r, err := pdf.New(*fonts)
	if err != nil {
		log.Fatal(err)
	}
	// Даты фиксированы, чтобы файл в репозитории не менялся от запуска к запуску.
	actDate := civil.New(2026, 9, 17)
	from, to := civil.New(2026, 9, 1), civil.New(2026, 9, 15)

	tbl := &pdf.Table{
		Columns: []string{"Наименование вида работы (услуги)", "Периодичность / количественный показатель", "Единица измерения", "Стоимость за единицу, руб.", "Цена, руб."},
		Widths:  []int{8, 5, 3, 4, 4},
	}
	var total domain.Kopecks
	for _, l := range d.Lines {
		price, _ := domain.ParseMoney(l.UnitPrice)
		amount, _ := domain.ParseMoney(l.Amount)
		total += amount
		tbl.Rows = append(tbl.Rows, []string{l.WorkName, l.PeriodicityQty, l.Unit, price.Rubles(), amount.Rubles()})
	}
	tbl.Footer = []string{"Итого", "", "", "", total.Rubles()}

	in := pdf.Input{
		Title:      "Акт № " + d.Act.Number,
		Watermark:  "ДЕМО — синтетические данные: дом, организация, люди и цены вымышлены",
		Heading:    "АКТ № " + d.Act.Number,
		Subheading: "приёмки оказанных услуг и (или) выполненных работ по содержанию и текущему ремонту общего имущества в многоквартирном доме",
		Place:      "г. " + d.House.City,
		Date:       actDate.Russian(),
		Paragraphs: []string{
			fmt.Sprintf("Собственники помещений в многоквартирном доме, расположенном по адресу: %s, именуемые в дальнейшем «Заказчик», в лице %s, являющегося собственником квартиры № %s, находящейся в данном многоквартирном доме, действующего на основании решения общего собрания собственников помещений от %s № %s, с одной стороны, и %s, именуемое в дальнейшем «Исполнитель», в лице %s, %s, действующего на основании %s, с другой стороны, совместно именуемые «Стороны», составили настоящий Акт о нижеследующем:",
				d.House.Address, d.Chairman.FullName, d.Chairman.ApartmentNo, d.Chairman.AuthorityDate.Russian(), d.Chairman.AuthorityNumber,
				d.Contract.ExecutorName, d.Act.ExecutorSignatoryName, d.Act.ExecutorSignatoryPosition, d.Act.ExecutorBasis),
			fmt.Sprintf("1. Исполнителем предъявлены к приёмке следующие оказанные на основании договора управления многоквартирным домом № %s от %s (далее — «Договор») услуги и (или) выполненные работы по содержанию и текущему ремонту общего имущества в многоквартирном доме, расположенном по адресу: %s:",
				d.Contract.Number, d.Contract.Date.Russian(), d.House.Address),
		},
		Sections: []pdf.Section{{Table: tbl}},
		Closing: []string{
			fmt.Sprintf("2. Всего за период с %s по %s выполнено работ (оказано услуг) на общую сумму %s (%s) рублей.", from.Russian(), to.Russian(), total.Rubles(), d.Act.TotalAmountWords),
			"3. Работы (услуги) выполнены (оказаны) полностью, в установленные сроки, с надлежащим качеством.",
			"4. Претензий по выполнению условий Договора Стороны друг к другу не имеют.",
			"Настоящий Акт составлен в 2-х экземплярах, имеющих одинаковую юридическую силу, по одному для каждой из Сторон.",
			fmt.Sprintf("Исполнитель — %s, %s ____________", d.Act.ExecutorSignatoryPosition, d.Act.ExecutorSignatoryName),
		},
		Signature: "Заказчик — председатель совета МКД",
		SignName:  d.Chairman.FullName,
		Footer:    "Форма: приказ Минстроя России от 26.10.2015 № 761/пр. Документ синтетический и создан для демонстрации сервиса «Приёмка».",
		Created:   time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC),
	}
	b, err := r.Render(in)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println("сохранено:", *out, len(b), "байт")
}
