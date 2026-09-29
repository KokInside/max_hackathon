#!/usr/bin/env python3
"""Сквозной прогон сценария README (шаги 3–16) через API локального стека.

Только локально: вход через X-Dev-User (DEV_AUTH=true). Нужны python3 и pdftotext (poppler-utils).
Каждый запуск — новый председатель и жилец, поэтому прогон можно повторять.
  python3 scripts/e2e.py [http://localhost:8080]
"""
import datetime, json, os, struct, subprocess, sys, tempfile, urllib.error, urllib.request, uuid, zlib

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080"
CH = str(700000 + int.from_bytes(os.urandom(2), "big"))  # новый председатель на каждый прогон
RES = str(int(CH) + 50000)
fails = []


def req(method, path, user, body=None, start="", raw=None, ctype=None):
    h = {"X-Dev-User": user}
    if start:
        h["X-Dev-Start-Param"] = start
    data = None
    if body is not None:
        data, h["Content-Type"] = json.dumps(body).encode(), "application/json"
    if raw is not None:
        data, h["Content-Type"] = raw, ctype
    r = urllib.request.Request(BASE + path, data=data, method=method, headers=h)
    try:
        with urllib.request.urlopen(r) as resp:
            b = resp.read()
            return resp.status, (json.loads(b) if b and resp.headers.get_content_type() == "application/json" else b)
    except urllib.error.HTTPError as e:
        b = e.read()
        try:
            return e.code, json.loads(b)
        except ValueError:
            return e.code, b


def check(step, cond, detail=""):
    print(("OK   " if cond else "FAIL ") + step + ("" if cond else f" — {detail}"))
    if not cond:
        fails.append(step)


def png():
    raw = b"".join(b"\x00" + b"\xff\x00\x00" * 8 for _ in range(8))
    chunk = lambda t, d: struct.pack(">I", len(d)) + t + d + struct.pack(">I", zlib.crc32(t + d) & 0xFFFFFFFF)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", 8, 8, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b"")


def multipart(fields, fname, content, ctype):
    bnd = uuid.uuid4().hex
    out = b""
    for k, v in fields.items():
        out += f"--{bnd}\r\nContent-Disposition: form-data; name=\"{k}\"\r\n\r\n{v}\r\n".encode()
    out += f"--{bnd}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{fname}\"\r\nContent-Type: {ctype}\r\n\r\n".encode()
    return out + content + f"\r\n--{bnd}--\r\n".encode(), f"multipart/form-data; boundary={bnd}"


today = datetime.date.today().isoformat()

# 3. Демо-акт (демо-профиль заполняется сам).
st, v = req("POST", "/api/v1/acts/demo", CH)
check("3 демо-акт создан, 8 строк, ДЕМО", st == 201 and len(v["lines"]) == 8 and v["act"]["is_demo"], st)
act = v["act"]["id"]
lines = sorted(v["lines"], key=lambda l: l["position"])

# 4. Получен сегодня, лично, два подписанных экземпляра → сроки и проверки УК.
st, v = req("PATCH", f"/api/v1/acts/{act}", CH, {"received_on": today, "received_channel": "in_person", "copies_received": 2, "executor_signed": True})
d = v.get("deadlines") or {}
check("4 дата получения → «на проверке», сроки 10/30", st == 200 and v["act"]["status"] == "in_review" and d.get("response_on") and d.get("silent_on"), f"{st} {v.get('act', {}).get('status')}")
check("4 у сроков есть основания и kind", all(c.get("basis") and c.get("kind") for c in d.get("items", [])) if d.get("items") else bool(v.get("checks")), json.dumps(d)[:200])
check("4 проверка УК: акт получен через 8 дн. после оформления", any("8" in c.get("text", "") for c in v["checks"]), [c.get("text") for c in v["checks"]])

# 6. Строка 6 «не выполнено» с комментарием, строка 2 «под сомнением» + фото.
roof, windows = lines[5], lines[1]
st, _ = req("PATCH", f"/api/v1/lines/{roof['id']}", CH, {"review_status": "not_done", "comment": "Снег с кровли не убирали: сосульки над подъездом 2."})
check("6 строка «Очистка кровли» — не выполнено", st == 200, st)
st, _ = req("PATCH", f"/api/v1/lines/{windows['id']}", CH, {"review_status": "doubtful", "comment": "Окна грязные на 3–5 этажах."})
check("6 строка «Мытьё окон» — под сомнением", st == 200, st)
body, ct = multipart({"note": "фото окон"}, "windows.png", png(), "image/png")
st, ev = req("POST", f"/api/v1/lines/{windows['id']}/evidence", CH, raw=body, ctype=ct)
check("6 фото-доказательство добавлено", st == 201, (st, ev))
st, v = req("GET", f"/api/v1/acts/{act}", CH)
check("6 оспаривается 14 100,00 ₽ (2 строки)", v["sums"]["disputed"] == "14100.00" and v["sums"]["disputed_count"] == 2, v["sums"])

# 7. Подсказки ПП № 290.
st, cat = req("GET", "/api/v1/catalog/works?q=%D1%83%D0%B1%D0%BE%D1%80%D0%BA", CH)
check("7 подсказки ПП № 290 по «уборк»", st == 200 and len(cat["items"]) > 0 and all(i.get("ref") for i in cat["items"]), st)

# 8–10. Приглашение, ответ жильца, агрегаты у председателя.
st, inv = req("POST", f"/api/v1/acts/{act}/invites", CH)
check("8 приглашение со startapp=inv_", st == 201 and "startapp=inv_" in inv["link"], st)
tok = inv["token"]
st, rv = req("GET", f"/api/v1/invites/{tok}", RES, start="inv_" + tok)
check("9 экран жильца: подъезды 1–4, строки без цен", st == 200 and rv["entrances_count"] == 4 and all("amount" not in l and "unit_price" not in l for l in rv["lines"]), st)
visible = [l["id"] for l in rv["lines"]]
st, _ = req("PUT", f"/api/v1/invites/{tok}/votes", RES, {"consent": True, "entrance_no": 2, "votes": [{"line_id": visible[0], "answer": "no", "comment": "не видел уборки"}]}, start="inv_" + tok)
check("9 ответ жильца принят", st == 200, st)
st, v = req("GET", f"/api/v1/acts/{act}", CH)
check("10 у председателя: ответили 1, агрегат по строке", v["respondents"] == 1 and any((l.get("residents") or {}).get("no") == 1 and any("не видел уборки" in c for c in (l.get("residents") or {}).get("comments", [])) for l in v["lines"]), (v["respondents"], [l.get("residents") for l in v["lines"]][:2]))
st, _ = req("GET", f"/api/v1/acts/{act}", RES)
check("10 жилец не видит карточку акта (403)", st == 403, st)

# 11. Перемотка к 9-му дню.
st, v = req("POST", f"/api/v1/acts/{act}/demo-shift", CH, {"to_day": 9})
check("11 перемотка к 9-му дню", st == 200 and v["act"]["demo_shift_days"] > 0, st)

# 12. Отказ → PDF.
st, dec = req("POST", f"/api/v1/acts/{act}/decision", CH, {"decision": "refuse"})
check("12 отказ сформирован", st == 201 and dec["document"]["kind"] == "refusal", (st, dec))
st, pdf = req("GET", dec["url"], CH)
text = ""
if st == 200:
    with tempfile.NamedTemporaryFile(suffix=".pdf") as f:
        f.write(pdf)
        f.flush()
        text = subprocess.run(["pdftotext", "-layout", f.name, "-"], capture_output=True, text=True).stdout
for want in ["Мотивированный отказ", "Очистка кровли", "Д-1", "ДЕМО", "14 100,00"]:
    check(f"12 в PDF есть «{want}»", want in text, "нет в тексте PDF")

# 13. Отметка отправки.
st, v = req("POST", f"/api/v1/acts/{act}/dispatch", CH, {"channel": "in_person", "sent_on": today})
check("13 отправка отмечена → «отказ направлен»", st == 201 and v["act"]["status"] == "refused_sent", (st, v.get("act", {}).get("status") if isinstance(v, dict) else v))
st, _ = req("POST", f"/api/v1/acts/{act}/dispatch", CH, {"channel": "post", "sent_on": today})
check("13 повторная отметка отправки → 409", st == 409, st)

# 14. Новый акт после отказа.
st, nv = req("POST", f"/api/v1/acts/{act}/successor", CH)
check("14 новый акт: строки скопированы, оспоренные помечены", st == 201 and len(nv["lines"]) == 8 and sum(1 for l in nv["lines"] if l["prev_disputed"]) == 2, (st, [l.get("prev_disputed") for l in nv.get("lines", [])]))
new = nv["act"]["id"]

# 15. Новый акт → к 31-му дню → «принят молчанием», изменения заблокированы.
req("PATCH", f"/api/v1/acts/{new}", CH, {"received_on": today})
st, v = req("POST", f"/api/v1/acts/{new}/demo-shift", CH, {"to_day": 31})
check("15 к 31-му дню → «принят молчанием»", st == 200 and v["act"]["status"] == "deemed_accepted" and not v["editable"], (st, v.get("act", {}).get("status")))
st, e = req("PATCH", f"/api/v1/acts/{new}", CH, {"number": "18"})
check("15 правка закрытого акта → 409 ACT_LOCKED", st == 409 and e["error"]["code"] == "ACT_LOCKED", (st, e))

# 16. Ошибки: неверная дата, отказ без возражений, файл не того типа, слишком большой файл.
st, e = req("POST", "/api/v1/acts/demo", CH)
fresh = e["act"]["id"]
st, e = req("PATCH", f"/api/v1/acts/{fresh}", CH, {"received_on": "вчера"})
check("16 дата текстом → 400", st == 400, (st, e))
req("PATCH", f"/api/v1/acts/{fresh}", CH, {"received_on": today})
st, e = req("POST", f"/api/v1/acts/{fresh}/decision", CH, {"decision": "refuse"})
check("16 отказ без возражений → 422 NO_OBJECTIONS", st == 422 and e["error"]["code"] == "NO_OBJECTIONS", (st, e))
body, ct = multipart({}, "a.txt", b"hello", "text/plain")
fl = sorted(req("GET", f"/api/v1/acts/{fresh}", CH)[1]["lines"], key=lambda l: l["position"])[0]["id"]
st, e = req("POST", f"/api/v1/lines/{fl}/evidence", CH, raw=body, ctype=ct)
check("16 файл не того типа → 415", st == 415, (st, e))
body, ct = multipart({}, "big.png", png() + b"\0" * (11 << 20), "image/png")
st, e = req("POST", f"/api/v1/lines/{fl}/evidence", CH, raw=body, ctype=ct)
check("16 фото больше 10 МБ → 413", st == 413, (st, e))

# Удаление демо-акта.
st, _ = req("DELETE", f"/api/v1/acts/{fresh}", CH)
check("удаление демо-акта → 204", st == 204, st)

print(f"\nИтого: {'всё OK' if not fails else 'FAIL: ' + ', '.join(fails)}")
sys.exit(1 if fails else 0)
