package api

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"

	"priemka/internal/app"
	"priemka/internal/domain"
	"priemka/internal/rules"
	"priemka/internal/store"
)

type meResponse struct {
	app.ProfileBundle
	Mode        string `json:"mode"`
	InviteToken string `json:"invite_token,omitempty"`
	DemoMode    bool   `json:"demo_mode"`
	BotUsername string `json:"bot_username"`
	Complete    bool   `json:"profile_complete"`
}

func (a *API) me(w http.ResponseWriter, r *http.Request, u store.User) error {
	b, err := a.svc.Profile(r.Context(), u.ID)
	if err != nil {
		return err
	}
	resp := meResponse{ProfileBundle: b, Mode: "chairman", DemoMode: a.svc.Config().DemoMode, BotUsername: a.svc.Config().BotUsername, Complete: b.Complete()}
	if tok, ok := strings.CutPrefix(startParam(r), app.InvitePrefix); ok {
		resp.Mode, resp.InviteToken = "resident", tok
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (a *API) saveProfile(w http.ResponseWriter, r *http.Request, u store.User) error {
	var in app.ProfileInput
	if err := decode(r, &in); err != nil {
		return err
	}
	b, err := a.svc.SaveProfile(r.Context(), u.ID, in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, b)
	return nil
}

func (a *API) listActs(w http.ResponseWriter, r *http.Request, u store.User) error {
	acts, err := a.svc.ActList(r.Context(), u.ID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"acts": acts})
	return nil
}

func (a *API) createDemoAct(w http.ResponseWriter, r *http.Request, u store.User) error {
	act, err := a.svc.CreateDemoAct(r.Context(), u.ID, nil)
	if err != nil {
		return err
	}
	v, err := a.svc.ActView(r.Context(), u.ID, act.ID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, v)
	return nil
}

func (a *API) actView(w http.ResponseWriter, r *http.Request, u store.User, status int) error {
	v, err := a.svc.ActView(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, status, v)
	return nil
}

func (a *API) getAct(w http.ResponseWriter, r *http.Request, u store.User) error {
	if err := validID(r.PathValue("id")); err != nil {
		return err
	}
	return a.actView(w, r, u, http.StatusOK)
}

func (a *API) patchAct(w http.ResponseWriter, r *http.Request, u store.User) error {
	if err := validID(r.PathValue("id")); err != nil {
		return err
	}
	var p app.HeaderPatch
	if err := decode(r, &p); err != nil {
		return err
	}
	if err := a.svc.UpdateHeader(r.Context(), u.ID, r.PathValue("id"), p); err != nil {
		return err
	}
	return a.actView(w, r, u, http.StatusOK)
}

func (a *API) addLine(w http.ResponseWriter, r *http.Request, u store.User) error {
	if err := validID(r.PathValue("id")); err != nil {
		return err
	}
	var in app.LineInput
	if err := decode(r, &in); err != nil {
		return err
	}
	l, err := a.svc.AddLine(r.Context(), u.ID, r.PathValue("id"), in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, l)
	return nil
}

func (a *API) patchLine(w http.ResponseWriter, r *http.Request, u store.User) error {
	if err := validID(r.PathValue("id")); err != nil {
		return err
	}
	var in app.LineInput
	if err := decode(r, &in); err != nil {
		return err
	}
	l, err := a.svc.UpdateLine(r.Context(), u.ID, r.PathValue("id"), in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, l)
	return nil
}

func (a *API) deleteLine(w http.ResponseWriter, r *http.Request, u store.User) error {
	if err := validID(r.PathValue("id")); err != nil {
		return err
	}
	if err := a.svc.DeleteLine(r.Context(), u.ID, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) addEvidence(w http.ResponseWriter, r *http.Request, u store.User) error {
	if err := validID(r.PathValue("id")); err != nil {
		return err
	}
	max := a.svc.Config().MaxPhoto
	r.Body = http.MaxBytesReader(w, r.Body, max+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		return &app.Error{Status: http.StatusUnsupportedMediaType, Code: "BAD_CONTENT_TYPE", Message: "Ожидается multipart/form-data с полем file."}
	}
	var note string
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				return &app.Error{Status: http.StatusRequestEntityTooLarge, Code: "FILE_TOO_LARGE", Message: fmt.Sprintf("Файл больше %d МБ.", max>>20)}
			}
			return &app.Error{Status: http.StatusBadRequest, Code: "BAD_MULTIPART", Message: "Не удалось прочитать загрузку. Повторите."}
		}
		switch part.FormName() {
		case "note":
			b, _ := io.ReadAll(io.LimitReader(part, 1000))
			note = string(b)
		case "file":
			ev, err := a.svc.AddEvidence(r.Context(), u.ID, r.PathValue("id"), part, path.Base(part.FileName()), note)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusCreated, ev)
			return nil
		}
	}
	return &app.Error{Status: http.StatusUnprocessableEntity, Code: "VALIDATION", Message: "Нет поля file с фото или PDF."}
}

func (a *API) deleteEvidence(w http.ResponseWriter, r *http.Request, u store.User) error {
	if err := validID(r.PathValue("id")); err != nil {
		return err
	}
	if err := a.svc.DeleteEvidence(r.Context(), u.ID, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) createInvite(w http.ResponseWriter, r *http.Request, u store.User) error {
	if err := validID(r.PathValue("id")); err != nil {
		return err
	}
	inv, err := a.svc.CreateInvite(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		return err
	}
	act, _, err := a.svc.ChairmanAct(r.Context(), u.ID, inv.ActID)
	if err != nil {
		return err
	}
	link := a.svc.InviteLink(inv.Token)
	writeJSON(w, http.StatusCreated, map[string]any{"token": inv.Token, "link": link, "text": a.svc.InviteText(act, link), "expires_at": inv.ExpiresAt})
	return nil
}

func (a *API) getInvite(w http.ResponseWriter, r *http.Request, u store.User) error {
	v, err := a.svc.ResidentView(r.Context(), u.ID, r.PathValue("token"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, v)
	return nil
}

func (a *API) putVotes(w http.ResponseWriter, r *http.Request, u store.User) error {
	var in app.VotesInput
	if err := decode(r, &in); err != nil {
		return err
	}
	v, err := a.svc.SubmitVotes(r.Context(), u.ID, r.PathValue("token"), in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, v)
	return nil
}

func (a *API) decide(w http.ResponseWriter, r *http.Request, u store.User) error {
	if err := validID(r.PathValue("id")); err != nil {
		return err
	}
	var in struct {
		Decision        domain.Decision `json:"decision"`
		ConfirmDisputed bool            `json:"confirm_disputed"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	res, err := a.svc.Decide(r.Context(), u.ID, r.PathValue("id"), in.Decision, in.ConfirmDisputed)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, res)
	return nil
}

func (a *API) dispatch(w http.ResponseWriter, r *http.Request, u store.User) error {
	if err := validID(r.PathValue("id")); err != nil {
		return err
	}
	var in app.DispatchInput
	if err := decode(r, &in); err != nil {
		return err
	}
	if _, err := a.svc.Dispatch(r.Context(), u.ID, r.PathValue("id"), in); err != nil {
		return err
	}
	return a.actView(w, r, u, http.StatusCreated)
}

func (a *API) successor(w http.ResponseWriter, r *http.Request, u store.User) error {
	if err := validID(r.PathValue("id")); err != nil {
		return err
	}
	act, err := a.svc.Successor(r.Context(), u.ID, r.PathValue("id"), nil)
	if err != nil {
		return err
	}
	v, err := a.svc.ActView(r.Context(), u.ID, act.ID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, v)
	return nil
}

func (a *API) demoShift(w http.ResponseWriter, r *http.Request, u store.User) error {
	if err := validID(r.PathValue("id")); err != nil {
		return err
	}
	var in struct {
		Days  int `json:"days"`
		ToDay int `json:"to_day"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.Days == 0 && in.ToDay == 0 {
		return &app.Error{Status: http.StatusUnprocessableEntity, Code: "VALIDATION", Message: "Укажите days или to_day."}
	}
	if _, err := a.svc.DemoShift(r.Context(), u.ID, r.PathValue("id"), in.Days, in.ToDay); err != nil {
		return err
	}
	return a.actView(w, r, u, http.StatusOK)
}

func (a *API) catalog(w http.ResponseWriter, r *http.Request, _ store.User) error {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < 2 || len([]rune(q)) > 100 {
		return &app.Error{Status: http.StatusUnprocessableEntity, Code: "VALIDATION", Message: "Параметр q: от 2 до 100 символов."}
	}
	c := a.svc.Rules().Catalog
	items := c.Search(q, 10)
	if items == nil {
		items = []rules.CatalogItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "source": a.svc.Rules().Sources[c.Source], "edition": c.Edition, "note": c.Note})
	return nil
}

func (a *API) rulesList(w http.ResponseWriter, r *http.Request) error {
	rs := a.svc.Rules()
	out := make([]any, 0, len(rs.Order))
	for _, id := range rs.Order {
		out = append(out, rs.Basis(id)[0])
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": rs.Version, "rules": out, "params": map[string]any{
		"day_type": rs.Params.DayType, "counting_start": rs.Params.CountingStart, "timezone": rs.Params.Timezone,
	}})
	return nil
}

// file отдаёт файл по подписанной ссылке (заголовок авторизации <img> передать не может).
func (a *API) file(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if err := validID(id); err != nil {
		return err
	}
	if err := a.svc.Files().Verify(id, r.URL.Query().Get("exp"), r.URL.Query().Get("sig")); err != nil {
		return &app.Error{Status: http.StatusForbidden, Code: "BAD_SIGNATURE", Message: "Ссылка на файл недействительна или устарела. Обновите страницу."}
	}
	f, err := a.svc.Store().Q().FileByID(r.Context(), id)
	if err != nil {
		return err
	}
	fh, err := a.svc.Files().Open(f.StorageKey)
	if err != nil {
		return err
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", f.Mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=600")
	disp := "inline"
	if r.URL.Query().Get("download") == "1" {
		disp = "attachment"
	}
	name := f.OriginalName
	if name == "" {
		name = "file"
	}
	cd := mime.FormatMediaType(disp, map[string]string{"filename": name})
	if cd == "" { // имя с недопустимыми символами
		cd = disp
	}
	w.Header().Set("Content-Disposition", cd)
	http.ServeContent(w, r, "", st.ModTime(), fh)
	return nil
}

var reUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// validID отсекает не-UUID до запроса к БД: несуществующий объект — 404.
func validID(id string) error {
	if !reUUID.MatchString(id) {
		return &app.Error{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Не найдено."}
	}
	return nil
}
