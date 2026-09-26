package bot

import (
	"regexp"
	"strconv"
	"strings"

	"priemka/internal/app"
	"priemka/internal/civil"
)

// Онбординг — последовательность вопросов; ответы копятся в session.Data и сохраняются в конце.
const (
	stName      = "onb_name"
	stApartment = "onb_apartment"
	stAuthType  = "onb_auth_type"
	stAuthDoc   = "onb_auth_doc"
	stAddress   = "onb_address"
	stEntrances = "onb_entrances"
	stExecutor  = "onb_executor"
	stContract  = "onb_contract"
	stApplic    = "onb_applicability"
)

func (s *session) startOnboarding() error {
	if err := s.setState(stName, map[string]string{}); err != nil {
		return err
	}
	var extra [][]button
	if s.b.svc.Config().DemoMode {
		extra = append(extra, row(cbBtn("🧪 Заполнить демо-профилем", "demo_profile")))
	}
	return s.send("Заполним профиль — это нужно один раз: данные попадут в шапку документов.\n\n"+
		"Шаг 1 из 8. Ваши фамилия, имя и отчество — как в решении собрания или доверенности.", kb(extra...))
}

var reDateNum = regexp.MustCompile(`(\d{1,2}\.\d{1,2}\.\d{4})`)

func (s *session) onboardingText(text string) error {
	d := s.sess.Data
	next := func(state, question string, k ...[]button) error {
		if err := s.setState(state, d); err != nil {
			return err
		}
		return s.send(question, kb(k...))
	}
	switch s.sess.State {
	case stName:
		if len([]rune(text)) < 3 || len([]rune(text)) > 200 {
			return s.send("Напишите ФИО полностью, например: Иванова Мария Петровна.", nil)
		}
		d["full_name"] = text
		return next(stApartment, "Шаг 2 из 8. Номер вашей квартиры в этом доме.")
	case stApartment:
		if len([]rune(text)) > 20 {
			return s.send("Номер квартиры слишком длинный. Например: 12 или 12А.", nil)
		}
		d["apartment"] = text
		return next(stAuthType, "Шаг 3 из 8. На каком основании вы подписываете акты?",
			row(cbBtn("Решение общего собрания", "auth:oss_decision")), row(cbBtn("Доверенности собственников", "auth:power_of_attorney")))
	case stAuthType:
		return s.send("Выберите вариант кнопкой выше.", nil)
	case stAuthDoc:
		if m := reDateNum.FindString(text); m != "" {
			date, err := civil.Parse(m)
			if err != nil {
				return s.send("Не понял дату. Пример: 10.03.2026 № 1", nil)
			}
			d["auth_date"] = date.String()
			num := strings.TrimSpace(strings.NewReplacer(m, "", "№", "", "N", "", "от", "").Replace(text))
			d["auth_number"] = num
		} else {
			d["auth_number"] = strings.TrimSpace(strings.TrimPrefix(text, "№"))
		}
		return s.askAddress()
	case stAddress:
		if len([]rune(text)) < 8 {
			return s.send("Напишите адрес полностью, например: г. Казань, ул. Баумана, д. 1", nil)
		}
		d["address"] = text
		d["city"] = cityOf(text)
		return next(stEntrances, "Шаг 6 из 8. Сколько подъездов в доме? Напишите число.")
	case stEntrances:
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > 50 {
			return s.send("Напишите число от 1 до 50.", nil)
		}
		d["entrances"] = text
		return next(stExecutor, "Шаг 7 из 8. Название управляющей компании (исполнителя), как в договоре.")
	case stExecutor:
		d["executor"] = text
		return next(stContract, "Шаг 8 из 8. Номер и дата договора управления, например: 15/2025 от 15.01.2025. Если не знаете — нажмите «Пропустить».",
			row(cbBtn("Пропустить", "skip")))
	case stContract:
		if m := reDateNum.FindString(text); m != "" {
			if date, err := civil.Parse(m); err == nil {
				d["contract_date"] = date.String()
			}
			d["contract_number"] = strings.TrimSpace(strings.NewReplacer(m, "", "№", "", " от", "").Replace(text))
		} else {
			d["contract_number"] = text
		}
		return s.askApplicability()
	case stApplic:
		return s.send("Выберите вариант кнопкой выше.", nil)
	}
	return nil
}

func (s *session) askAddress() error {
	if err := s.setState(stAddress, s.sess.Data); err != nil {
		return err
	}
	return s.send("Шаг 5 из 8. Адрес дома полностью, например: г. Казань, ул. Баумана, д. 1", nil)
}

func (s *session) askApplicability() error {
	if err := s.setState(stApplic, s.sess.Data); err != nil {
		return err
	}
	return s.send("Последний вопрос. Порядок приёмки 318/пр применяется там, где совет дома обязателен (нет ТСЖ или кооператива, квартир больше четырёх) и председатель наделён полномочиями подписывать акты. Это ваш случай?",
		kb(row(cbBtn("Да", "applic:yes"), cbBtn("Нет или не уверен", "applic:no"))))
}

// cityOf выделяет город из адреса вида «г. Казань, ул. …».
func cityOf(addr string) string {
	first, _, _ := strings.Cut(addr, ",")
	first = strings.TrimSpace(first)
	for _, p := range []string{"г. ", "г.", "город "} {
		if c, ok := strings.CutPrefix(first, p); ok {
			return strings.TrimSpace(c)
		}
	}
	return ""
}

func (s *session) onboardingCallback(cmd, arg string) error {
	d := s.sess.Data
	switch cmd {
	case "auth":
		if s.sess.State != stAuthType {
			return nil
		}
		d["auth_type"] = arg
		if err := s.setState(stAuthDoc, d); err != nil {
			return err
		}
		return s.send("Шаг 4 из 8. Дата и номер решения собрания или доверенности, например: 10.03.2026 № 1.",
			kb(row(cbBtn("Пропустить", "skip"))))
	case "skip":
		switch s.sess.State {
		case stAuthDoc:
			return s.askAddress()
		case stContract:
			return s.askApplicability()
		}
		return nil
	case "applic":
		if s.sess.State != stApplic {
			return nil
		}
		applicable := arg == "yes"
		entrances, _ := strconv.Atoi(d["entrances"])
		authDate, _ := civil.Parse(d["auth_date"])
		contractDate, _ := civil.Parse(d["contract_date"])
		_, err := s.b.svc.SaveProfile(s.ctx, s.user.ID, app.ProfileInput{
			FullName: d["full_name"], ApartmentNo: d["apartment"], AuthorityType: d["auth_type"], AuthorityDate: authDate,
			AuthorityNumber: d["auth_number"], Applicable: &applicable, City: d["city"], Address: d["address"],
			EntrancesCount: entrances, ExecutorName: d["executor"], ContractNumber: d["contract_number"], ContractDate: contractDate,
		})
		if err != nil {
			return err
		}
		if err := s.clearState(); err != nil {
			return err
		}
		note := ""
		if !applicable {
			note = "\n\nПорядок 318/пр может к вам не применяться. Сервисом можно пользоваться добровольно: сроки и документы останутся полезными для переписки с УК."
		}
		if err := s.send("Профиль сохранён."+note, nil); err != nil {
			return err
		}
		return s.menu()
	}
	return nil
}
