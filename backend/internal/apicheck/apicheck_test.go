package apicheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	var doc any
	_ = json.Unmarshal([]byte(`{"acts":[{"id":"a1","lines":[{"id":"l1"}]}],"error":{"code":"X"},"n":null}`), &doc)
	for path, want := range map[string]any{"acts[0].id": "a1", "acts[0].lines[0].id": "l1", "error.code": "X", "n": nil} {
		if got, ok := Lookup(doc, path); !ok || got != want {
			t.Errorf("%s: %v %v", path, got, ok)
		}
	}
	for _, path := range []string{"acts[1].id", "acts.id", "error.code.x", "missing", "acts[x]"} {
		if _, ok := Lookup(doc, path); ok {
			t.Errorf("%s: найдено", path)
		}
	}
}

func TestLoadRepoSpec(t *testing.T) {
	s, err := Load(filepath.Join("..", "..", "..", "DATA-API.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Checks) == 0 || s.Roles["chairman"].Auth.Type != "header" {
		t.Fatalf("%+v", s)
	}
}

func TestLoadRejects(t *testing.T) {
	head := "schema_version: \"1\"\nsolution: x\nbase_url: https://x/api\nroles: {chairman: {auth: {type: none}}}\nchecks:\n"
	cases := map[string]string{
		"не сохраняется":  "  - {id: a, method: GET, path: \"/a/{id}\", params: {path: {id: \"${act_id}\"}}, role: chairman, expected_status: [200], response: {content_type: application/json}}\n",
		"path-параметра":  "  - {id: a, method: GET, path: \"/a/{id}\", role: chairman, expected_status: [200], response: {content_type: application/json}}\n",
		"не описана":      "  - {id: a, method: GET, path: /a, role: judge, expected_status: [200], response: {content_type: application/json}}\n",
		"метод":           "  - {id: a, method: FETCH, path: /a, role: chairman, expected_status: [200], response: {content_type: application/json}}\n",
		"повторный id":    "  - {id: a, method: GET, path: /a, role: chairman, expected_status: [200], response: {content_type: application/json}}\n  - {id: a, method: GET, path: /b, role: chairman, expected_status: [200], response: {content_type: application/json}}\n",
		"expected_status": "  - {id: a, method: GET, path: /a, role: chairman, response: {content_type: application/json}}\n",
		"extra":           "  - {id: a, method: GET, path: /a, role: chairman, expected_status: [200], response: {content_type: application/json}, extra: 1}\n",
	}
	for want, checks := range cases {
		p := filepath.Join(t.TempDir(), "spec.yaml")
		if err := os.WriteFile(p, []byte(head+checks), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(p)
		if err == nil {
			t.Errorf("%s: файл принят", want)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}
