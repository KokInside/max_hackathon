package bot

import (
	"testing"
	"time"

	"github.com/max-messenger/max-bot-api-client-go/v2/model"
)

func TestDedup(t *testing.T) {
	var d dedup
	now := time.Now()
	if !d.first("m:1", now) || d.first("m:1", now.Add(time.Minute)) {
		t.Fatal("повтор в пределах TTL должен отсекаться")
	}
	if !d.first("m:1", now.Add(dedupTTL+time.Second)) {
		t.Fatal("после TTL событие снова считается новым")
	}
	if !d.first("", now) || !d.first("", now) {
		t.Fatal("события без ключа не отсекаются")
	}
}

func TestUpdateKey(t *testing.T) {
	msg := model.Update{UpdateType: model.UpdateMessageCreated, Message: &model.MessageUpdate{Body: model.MessageBody{Mid: "mid.1"}}}
	cb := model.Update{UpdateType: model.UpdateMessageCallback, Callback: &model.Callback{CallbackID: "cb.1"}}
	st := model.Update{UpdateType: model.UpdateBotStarted, Timestamp: 5, User: &model.User{UserID: 7}}
	if updateKey(msg) != "m:mid.1" || updateKey(cb) != "c:cb.1" || updateKey(st) != "s:7:5" {
		t.Fatal(updateKey(msg), updateKey(cb), updateKey(st))
	}
}
