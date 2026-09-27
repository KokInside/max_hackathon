package bot

import (
	"github.com/max-messenger/max-bot-api-client-go/v2/model"

	"priemka/internal/app"
	"priemka/internal/domain"
	"priemka/internal/store"
)

type button = model.Button

func cbBtn(text, payload string) button {
	return button{Type: model.ButtonCallback, Text: text, Payload: payload}
}

// appBtn открывает мини-приложение бота; payload попадает в start_param.
func (b *Bot) appBtn(text, payload string) button {
	return button{Type: model.ButtonOpenApp, Text: text, WebApp: b.c.Username, ContactID: b.c.BotID, Payload: payload}
}

func row(bs ...button) []button { return bs }

func kb(rows ...[]button) *model.Keyboard {
	if len(rows) == 0 {
		return nil
	}
	k := model.NewKeyboard()
	for _, r := range rows {
		kr := k.AddRow()
		for _, b := range r {
			kr.AddButton(b)
		}
	}
	return k
}

func menuKeyboard(demo bool) *model.Keyboard {
	rows := [][]button{row(cbBtn("📄 Получил акт от УК", "act_new"))}
	if demo {
		rows = append(rows, row(cbBtn("🧪 Взять демо-акт", "act_demo")))
	}
	rows = append(rows, row(cbBtn("Профиль", "profile"), cbBtn("Как это работает", "help")))
	return kb(rows...)
}

// actKeyboard — действия по текущему акту в зависимости от статуса.
func actKeyboard(b *Bot, a store.Act, demo bool) *model.Keyboard {
	var rows [][]button
	switch a.Status {
	case domain.StatusDraft:
		rows = append(rows, row(cbBtn("📅 Указать дату получения", "recv:"+a.ID)))
	case domain.StatusInReview, domain.StatusDecided:
		rows = append(rows, row(b.appBtn("🔍 Открыть проверку", "act_"+a.ID)))
		rows = append(rows, row(cbBtn("👥 Пригласить жильцов", "inv:"+a.ID)))
		if a.Status == domain.StatusDecided {
			rows = append(rows, row(cbBtn("📮 Отметить отправку в УК", "disp:"+a.ID)))
		}
	case domain.StatusRefusedSent:
		rows = append(rows, row(cbBtn("📄 Получил новый акт", "next:"+a.ID)))
		rows = append(rows, row(b.appBtn("Открыть акт", "act_"+a.ID)))
	default:
		rows = append(rows, row(b.appBtn("Открыть акт", "act_"+a.ID)))
	}
	if demo && a.IsDemo && !a.ReceivedOn.IsZero() && a.Status.Editable() {
		rows = append(rows, row(cbBtn("🧪 Демо: перемотать время", "demo:"+a.ID)))
	}
	if app.ActDeletable(a) {
		rows = append(rows, row(cbBtn("🗑 Удалить акт", "adel:"+a.ID)))
	}
	rows = append(rows, row(cbBtn("📄 Другой акт", "act_new"), cbBtn("Меню", "menu")))
	return kb(rows...)
}
