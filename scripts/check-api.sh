#!/usr/bin/env bash
# Прогон обязательных проверок из DATA-API.yaml против развёрнутого API.
# Использование: scripts/check-api.sh https://<домен>/api/v1 <CHAIRMAN_TOKEN> <RESIDENT_TOKEN>
# Локально: scripts/check-api.sh http://localhost:8080/api/v1 <токены из TEST_ACCOUNTS>
set -u
B=${1:?base url}; CH=${2:?chairman token}; RE=${3:?resident token}
J='Content-Type: application/json'
fail=0

req() { # method path role [body] -> печатает «код тело»
  local m=$1 p=$2 role=$3 body=${4:-} auth=()
  case $role in chairman) auth=(-H "Authorization: Bearer $CH");; resident) auth=(-H "Authorization: Bearer $RE");; esac
  if [ -n "$body" ]; then curl -s -X "$m" "${auth[@]}" -H "$J" -d "$body" -w '\n%{http_code}' "$B$p"
  else curl -s -X "$m" "${auth[@]}" -w '\n%{http_code}' "$B$p"; fi
}
check() { # id expected_codes response
  local id=$1 want=$2 resp=$3 code
  code=$(tail -n1 <<<"$resp")
  if [[ " $want " == *" $code "* ]]; then echo "OK   $id ($code)"; else echo "FAIL $id: получен $code, ожидался $want"; fail=1; fi
}
body() { sed '$d' <<<"$1"; }
field() { python3 -c "import json,sys;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1"; }

r=$(req GET /me chairman);                      check me "200" "$r"
r=$(req GET /acts chairman);                    check acts_list "200" "$r"
ACT=$(body "$r" | field '["acts"][0]["id"]' 2>/dev/null)
r=$(req GET "/acts/$ACT" chairman);             check act_view "200" "$r"
LINE=$(body "$r" | field '["lines"][0]["id"]' 2>/dev/null)
r=$(req GET "/acts/$ACT" none);                 check act_view_unauthorized "401" "$r"
r=$(req GET "/acts/$ACT" resident);             check act_view_foreign "403" "$r"
r=$(req PATCH "/lines/$LINE" chairman '{"review_status":"not_done","comment":"Проверка API: работа не выполнена"}'); check line_review "200 409" "$r"
r=$(req PATCH "/lines/$LINE" chairman '{"review_status":"maybe"}'); check line_review_invalid "422 409" "$r"
r=$(req GET "/catalog/works?q=%D0%BC%D1%8B%D1%82%D1%8C%D0%B5%20%D0%BE%D0%BA%D0%BE%D0%BD" chairman); check catalog "200" "$r"
r=$(req POST "/acts/$ACT/invites" chairman);    check invite "201 409" "$r"
TOK=$(body "$r" | field '["token"]' 2>/dev/null)
r=$(req GET "/invites/$TOK" resident);          check resident_view "200" "$r"
r=$(req PUT "/invites/$TOK/votes" resident "{\"consent\":true,\"entrance_no\":1,\"votes\":[{\"line_id\":\"$LINE\",\"answer\":\"no\",\"comment\":\"Проверка API\"}]}"); check resident_votes "200 409 422" "$r"
r=$(req POST "/acts/$ACT/decision" chairman '{"decision":"refuse"}'); check decision_refuse "201 409" "$r"
r=$(req GET /rules none);                       check rules "200" "$r"
exit $fail
