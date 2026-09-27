// Package apicheck выполняет обязательные проверки собственного API по DATA-API.yaml.
// Один и тот же код гоняет проверки против прода (cmd/checkapi) и в тестах против настоящего обработчика,
// поэтому файл проверок и API не расходятся.
package apicheck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Spec — содержимое DATA-API.yaml.
type Spec struct {
	SchemaVersion string          `yaml:"schema_version"`
	Solution      string          `yaml:"solution"`
	BaseURL       string          `yaml:"base_url"`
	OpenAPI       string          `yaml:"openapi"`
	Roles         map[string]Role `yaml:"roles"`
	Checks        []Check         `yaml:"checks"`
}

type Role struct {
	Description string `yaml:"description"`
	Auth        struct {
		Type  string `yaml:"type"` // header | none
		Name  string `yaml:"name"`
		Value string `yaml:"value"` // «Bearer <CHAIRMAN_TOKEN>»: <…> заменяется токеном роли
	} `yaml:"auth"`
}

type Check struct {
	ID          string `yaml:"id"`
	Description string `yaml:"description"`
	Method      string `yaml:"method"`
	Path        string `yaml:"path"`
	Params      struct {
		Path    map[string]string `yaml:"path"`
		Query   map[string]string `yaml:"query"`
		Headers map[string]string `yaml:"headers"`
		Body    any               `yaml:"body"`
	} `yaml:"params"`
	Role           string `yaml:"role"`
	ExpectedStatus []int  `yaml:"expected_status"`
	Response       struct {
		ContentType string   `yaml:"content_type"`
		Schema      string   `yaml:"schema"`
		Required    []string `yaml:"required"`
	} `yaml:"response"`
	Save map[string]string `yaml:"save"`
}

// Load читает и проверяет DATA-API.yaml.
func Load(path string) (*Spec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Spec
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, s.validate()
}

var methods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}

func (s *Spec) validate() error {
	if s.SchemaVersion == "" || s.Solution == "" || s.BaseURL == "" || len(s.Checks) == 0 {
		return fmt.Errorf("DATA-API.yaml: нужны schema_version, solution, base_url и checks")
	}
	ids := map[string]bool{}
	saved := map[string]bool{}
	for _, c := range s.Checks {
		switch {
		case c.ID == "" || ids[c.ID]:
			return fmt.Errorf("проверка %q: пустой или повторный id", c.ID)
		case !methods[c.Method]:
			return fmt.Errorf("проверка %s: метод %q", c.ID, c.Method)
		case !strings.HasPrefix(c.Path, "/"):
			return fmt.Errorf("проверка %s: путь должен начинаться с /", c.ID)
		case len(c.ExpectedStatus) == 0 || c.Response.ContentType == "":
			return fmt.Errorf("проверка %s: нужны expected_status и response.content_type", c.ID)
		}
		if _, ok := s.Roles[c.Role]; !ok {
			return fmt.Errorf("проверка %s: роль %q не описана в roles", c.ID, c.Role)
		}
		for _, name := range reTemplate.FindAllStringSubmatch(c.Path, -1) {
			if _, ok := c.Params.Path[name[1]]; !ok {
				return fmt.Errorf("проверка %s: нет значения path-параметра %s", c.ID, name[1])
			}
		}
		// Переменные должны быть сохранены одной из предыдущих проверок.
		for _, v := range reVar.FindAllStringSubmatch(fmt.Sprint(c.Params.Path, c.Params.Query, c.Params.Headers, c.Params.Body), -1) {
			if !saved[v[1]] {
				return fmt.Errorf("проверка %s: переменная ${%s} не сохраняется предыдущими проверками", c.ID, v[1])
			}
		}
		ids[c.ID] = true
		for k := range c.Save {
			saved[k] = true
		}
	}
	return nil
}

var (
	reTemplate    = regexp.MustCompile(`\{([a-z_]+)\}`)
	reVar         = regexp.MustCompile(`\$\{([a-z_]+)\}`)
	rePlaceholder = regexp.MustCompile(`<[A-Z_]+>`)
)

// Options — куда и с какими токенами ходить.
type Options struct {
	BaseURL string            // без завершающего «/», например https://домен/api/v1
	Tokens  map[string]string // роль → токен тестовой учётки
	Client  *http.Client
	// Observe (необязательно) получает каждый обмен — тесты сверяют его с openapi.yaml.
	Observe func(c Check, req *http.Request, reqBody []byte, resp *http.Response, respBody []byte)
}

// Result — итог одной проверки.
type Result struct {
	ID     string
	Status int
	Err    error
}

// Run выполняет проверки по порядку. Значения из save подставляются в следующие проверки как ${имя}.
func (s *Spec) Run(ctx context.Context, o Options) []Result {
	if o.Client == nil {
		o.Client = http.DefaultClient
	}
	vars := map[string]string{}
	out := make([]Result, 0, len(s.Checks))
	for _, c := range s.Checks {
		st, err := s.run(ctx, o, c, vars)
		out = append(out, Result{ID: c.ID, Status: st, Err: err})
	}
	return out
}

func (s *Spec) run(ctx context.Context, o Options, c Check, vars map[string]string) (int, error) {
	var missing []string
	subst := func(v string) string {
		return reVar.ReplaceAllStringFunc(v, func(m string) string {
			name := m[2 : len(m)-1]
			val, ok := vars[name]
			if !ok {
				missing = append(missing, name)
			}
			return val
		})
	}
	path := reTemplate.ReplaceAllStringFunc(c.Path, func(m string) string {
		return url.PathEscape(subst(c.Params.Path[m[1:len(m)-1]]))
	})
	u := strings.TrimRight(o.BaseURL, "/") + path
	if len(c.Params.Query) > 0 {
		q := url.Values{}
		for k, v := range c.Params.Query {
			q.Set(k, subst(v))
		}
		u += "?" + q.Encode()
	}
	var body []byte
	if c.Params.Body != nil {
		b, err := json.Marshal(substAny(c.Params.Body, subst))
		if err != nil {
			return 0, err
		}
		body = b
	}
	if len(missing) > 0 {
		return 0, fmt.Errorf("нет значений %s: предыдущая проверка их не сохранила", strings.Join(missing, ", "))
	}
	req, err := http.NewRequestWithContext(ctx, c.Method, u, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	for k, v := range c.Params.Headers {
		req.Header.Set(k, subst(v))
	}
	if a := s.Roles[c.Role].Auth; a.Type == "header" {
		tok := o.Tokens[c.Role]
		if tok == "" {
			return 0, fmt.Errorf("не задан токен роли %s", c.Role)
		}
		req.Header.Set(a.Name, rePlaceholder.ReplaceAllLiteralString(a.Value, tok))
	}
	resp, err := o.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if o.Observe != nil {
		o.Observe(c, req, body, resp, respBody)
	}

	if !slices.Contains(c.ExpectedStatus, resp.StatusCode) {
		return resp.StatusCode, fmt.Errorf("код %d, ожидался %v: %.300s", resp.StatusCode, c.ExpectedStatus, bytes.TrimSpace(respBody))
	}
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt != c.Response.ContentType {
		return resp.StatusCode, fmt.Errorf("Content-Type %q, ожидался %s", mt, c.Response.ContentType)
	}
	if c.Response.ContentType != "application/json" {
		return resp.StatusCode, nil
	}
	var doc any
	if err := json.Unmarshal(respBody, &doc); err != nil {
		return resp.StatusCode, fmt.Errorf("ответ не JSON: %w", err)
	}
	for _, f := range c.Response.Required {
		if _, ok := Lookup(doc, f); !ok {
			return resp.StatusCode, fmt.Errorf("нет обязательного поля %s", f)
		}
	}
	for name, f := range c.Save {
		v, ok := Lookup(doc, f)
		if !ok || v == nil {
			return resp.StatusCode, fmt.Errorf("нет поля %s для ${%s}", f, name)
		}
		vars[name] = fmt.Sprint(v)
	}
	return resp.StatusCode, nil
}

// Lookup достаёт значение по пути вида «acts[0].id» или «error.code».
func Lookup(doc any, path string) (any, bool) {
	cur := doc
	for part := range strings.SplitSeq(path, ".") {
		name, idx, _ := strings.Cut(part, "[")
		if name != "" {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, false
			}
			if cur, ok = m[name]; !ok {
				return nil, false
			}
		}
		for idx != "" {
			n, rest, _ := strings.Cut(idx, "]")
			i, err := strconv.Atoi(n)
			arr, ok := cur.([]any)
			if err != nil || !ok || i < 0 || i >= len(arr) {
				return nil, false
			}
			cur = arr[i]
			idx = strings.TrimPrefix(rest, "[")
		}
	}
	return cur, true
}

// substAny подставляет переменные во все строки тела запроса.
func substAny(v any, f func(string) string) any {
	switch t := v.(type) {
	case string:
		return f(t)
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, x := range t {
			m[k] = substAny(x, f)
		}
		return m
	case []any:
		a := make([]any, len(t))
		for i, x := range t {
			a[i] = substAny(x, f)
		}
		return a
	}
	return v
}
