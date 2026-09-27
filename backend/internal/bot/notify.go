package bot

import (
	"context"
	"fmt"

	"priemka/internal/domain"
	"priemka/internal/maxbot"
	"priemka/internal/store"
)

// Notify — напоминание по акту (app.Notifier).
func (b *Bot) Notify(ctx context.Context, chairmanMaxID int64, actID, text string) error {
	if chairmanMaxID <= 0 {
		return nil // тестовые учётки без аккаунта MAX
	}
	a, err := b.svc.Store().Q().ActByID(ctx, actID)
	if err != nil {
		return err
	}
	_, err = b.c.Send(ctx, maxbot.Outgoing{To: maxbot.Recipient{UserID: chairmanMaxID}, Text: text, Keyboard: actKeyboard(b, a, b.svc.Config().DemoMode)})
	return err
}

// SendDocument загружает PDF в MAX и присылает его председателю (app.Notifier).
func (b *Bot) SendDocument(ctx context.Context, chairmanMaxID int64, a store.Act, doc store.Document, path, name string) (string, error) {
	if chairmanMaxID <= 0 {
		return "", nil
	}
	token, err := b.c.UploadFile(ctx, path, name)
	if err != nil {
		return "", err
	}
	what := "Мотивированный отказ от подписания акта"
	next := "Распечатайте, подпишите и направьте исполнителю согласованным способом, позволяющим подтвердить получение (почта, e-mail, личный кабинет, лично). Затем отметьте отправку."
	if a.Decision == domain.DecisionSign {
		what = "Сопроводительное письмо к подписанному акту"
		next = "Подпишите акт и письмо и верните исполнителю один экземпляр акта. Затем отметьте отправку."
	}
	text := fmt.Sprintf("📎 %s № %s (версия %d).\n\n%s\n\nДокумент — шаблон: правовые формулировки не проверены юристом, прочитайте перед подписанием.", what, a.DisplayNumber(), doc.Version, next)
	if a.IsDemo {
		text += "\n\n🧪 ДЕМО: документ содержит синтетические данные."
	}
	return b.c.Send(ctx, maxbot.Outgoing{To: maxbot.Recipient{UserID: chairmanMaxID}, Text: text, FileToken: token,
		Keyboard: kb(row(cbBtn("📮 Отметить отправку в УК", "disp:"+a.ID)), row(b.appBtn("Открыть акт", "act_"+a.ID)))})
}
