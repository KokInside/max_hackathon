package rules

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"go.yaml.in/yaml/v3"
)

// Texts — шаблоны сообщений бота и текстов документов из config/templates.
type Texts struct {
	msgs map[string]*template.Template
	docs map[string]*template.Template // ключ «раздел.поле» или «раздел.поле.N» для списков
	lens map[string]int                // длина списков
}

func LoadTexts(dir string) (*Texts, error) {
	t := &Texts{msgs: map[string]*template.Template{}, docs: map[string]*template.Template{}, lens: map[string]int{}}

	var msgs map[string]string
	if err := readYAML(filepath.Join(dir, "templates", "messages.yaml"), &msgs); err != nil {
		return nil, err
	}
	for k, v := range msgs {
		tpl, err := template.New(k).Option("missingkey=error").Funcs(funcs).Parse(strings.TrimRight(v, "\n"))
		if err != nil {
			return nil, fmt.Errorf("messages.yaml %s: %w", k, err)
		}
		t.msgs[k] = tpl
	}

	var docs map[string]map[string]any
	if err := readYAML(filepath.Join(dir, "templates", "documents.yaml"), &docs); err != nil {
		return nil, err
	}
	for sec, fields := range docs {
		for k, v := range fields {
			key := sec + "." + k
			switch val := v.(type) {
			case string:
				if err := t.addDoc(key, val); err != nil {
					return nil, err
				}
			case []any:
				t.lens[key] = len(val)
				for i, item := range val {
					if err := t.addDoc(fmt.Sprintf("%s.%d", key, i), fmt.Sprint(item)); err != nil {
						return nil, err
					}
				}
			default:
				return nil, fmt.Errorf("documents.yaml %s: неподдерживаемый тип", key)
			}
		}
	}
	return t, nil
}

func (t *Texts) addDoc(key, src string) error {
	tpl, err := template.New(key).Option("missingkey=error").Funcs(funcs).Parse(strings.TrimRight(src, "\n"))
	if err != nil {
		return fmt.Errorf("documents.yaml %s: %w", key, err)
	}
	t.docs[key] = tpl
	return nil
}

var funcs = template.FuncMap{"plural": Plural}

// Plural выбирает форму слова по числу: 1 день, 2 дня, 5 дней.
func Plural(n int, one, few, many string) string {
	if n < 0 {
		n = -n
	}
	switch a, b := n%100, n%10; {
	case a > 10 && a < 20:
		return many
	case b == 1:
		return one
	case b > 1 && b < 5:
		return few
	}
	return many
}

func readYAML(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}

func exec(tpl *template.Template, data any) (string, error) {
	var b bytes.Buffer
	if err := tpl.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// HasMsg — есть ли шаблон сообщения.
func (t *Texts) HasMsg(key string) bool { _, ok := t.msgs[key]; return ok }

// Msg рендерит сообщение бота.
func (t *Texts) Msg(key string, data any) (string, error) {
	tpl, ok := t.msgs[key]
	if !ok {
		return "", fmt.Errorf("нет шаблона сообщения %s", key)
	}
	return exec(tpl, data)
}

// Doc рендерит строковое поле документа («refusal.title»).
func (t *Texts) Doc(key string, data any) (string, error) {
	tpl, ok := t.docs[key]
	if !ok {
		return "", fmt.Errorf("нет текста документа %s", key)
	}
	return exec(tpl, data)
}

// DocList рендерит список абзацев («refusal.intro»).
func (t *Texts) DocList(key string, data any) ([]string, error) {
	n, ok := t.lens[key]
	if !ok {
		return nil, fmt.Errorf("нет списка %s", key)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		s, err := t.Doc(fmt.Sprintf("%s.%d", key, i), data)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
