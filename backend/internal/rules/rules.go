// Package rules загружает правила приёмки, справочник работ и шаблоны из каталога config/.
// Ошибка в конфиге останавливает запуск: работать на неполных правилах нельзя.
package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"priemka/internal/civil"
	"priemka/internal/domain"
)

type Source struct {
	ID            string `yaml:"-" json:"id"`
	Title         string `yaml:"title" json:"title"`
	Short         string `yaml:"short" json:"short"`
	Cite          string `yaml:"cite" json:"cite"`
	Edition       string `yaml:"edition" json:"edition"`
	EffectiveFrom string `yaml:"effective_from" json:"effective_from,omitempty"`
	EffectiveTo   string `yaml:"effective_to" json:"effective_to,omitempty"`
	URL           string `yaml:"url" json:"url"`
	CheckedAt     string `yaml:"checked_at" json:"checked_at"`
	Verified      bool   `yaml:"verified" json:"verified"`
}

type Rule struct {
	ID      string      `yaml:"id" json:"id"`
	Kind    domain.Kind `yaml:"kind" json:"kind"`
	Value   any         `yaml:"value" json:"value,omitempty"`
	Source  string      `yaml:"source" json:"source,omitempty"`
	Point   *string     `yaml:"point" json:"point"`
	Summary string      `yaml:"summary" json:"summary"`
}

type Reminder struct {
	Day      int    `yaml:"day" json:"day"`
	Kind     string `yaml:"kind" json:"kind"`
	Template string `yaml:"template" json:"template"`
}

type Params struct {
	DayType           string `yaml:"day_type"`
	CountingStart     string `yaml:"counting_start"`
	ShiftIfNonWorking bool   `yaml:"shift_if_non_working"`
	Timezone          string `yaml:"timezone"`
	ReminderHour      *int   `yaml:"reminder_hour"`
}

type file struct {
	Version   string            `yaml:"version"`
	Sources   map[string]Source `yaml:"sources"`
	Params    Params            `yaml:"params"`
	Rules     []Rule            `yaml:"rules"`
	Reminders []Reminder        `yaml:"reminders"`
}

// Set — загруженный и проверенный набор правил.
type Set struct {
	Version   string
	Sources   map[string]Source
	Rules     map[string]Rule
	Order     []string
	Reminders []Reminder
	Params    Params
	Location  *time.Location
	Policy    domain.Policy
	Catalog   *Catalog
}

// Load читает config/rules/acceptance.yaml и config/catalog/pp290.json.
func Load(dir string) (*Set, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "rules", "acceptance.yaml"))
	if err != nil {
		return nil, err
	}
	var f file
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("acceptance.yaml: %w", err)
	}
	s := &Set{Version: f.Version, Sources: map[string]Source{}, Rules: map[string]Rule{}, Reminders: f.Reminders, Params: f.Params}
	var errs []error
	if f.Version == "" {
		errs = append(errs, errors.New("не указана version"))
	}
	for id, src := range f.Sources {
		src.ID = id
		s.Sources[id] = src
	}
	for _, r := range f.Rules {
		if _, dup := s.Rules[r.ID]; dup {
			errs = append(errs, fmt.Errorf("правило %s объявлено дважды", r.ID))
		}
		switch r.Kind {
		case domain.KindNorm, domain.KindCalculation, domain.KindRecommendation, domain.KindAssumption:
		default:
			errs = append(errs, fmt.Errorf("правило %s: неизвестный kind %q", r.ID, r.Kind))
		}
		if r.Source != "" {
			if _, ok := s.Sources[r.Source]; !ok {
				errs = append(errs, fmt.Errorf("правило %s: нет источника %s", r.ID, r.Source))
			}
		}
		if src, ok := s.Sources[r.Source]; ok && src.Cite == "" {
			errs = append(errs, fmt.Errorf("источник %s без cite", r.Source))
		}
		if r.Kind == domain.KindNorm && r.Source == "" {
			errs = append(errs, fmt.Errorf("норма %s без источника", r.ID))
		}
		s.Rules[r.ID] = r
		s.Order = append(s.Order, r.ID)
	}

	if f.Params.DayType != "calendar" {
		errs = append(errs, fmt.Errorf("day_type=%q не поддерживается: в MVP только calendar", f.Params.DayType))
	}
	if f.Params.CountingStart != "next_day" && f.Params.CountingStart != "same_day" {
		errs = append(errs, fmt.Errorf("counting_start=%q: ожидается next_day или same_day", f.Params.CountingStart))
	}
	if f.Params.ShiftIfNonWorking {
		errs = append(errs, errors.New("shift_if_non_working=true в MVP не поддерживается: нужен производственный календарь"))
	}
	if f.Params.ReminderHour == nil || *f.Params.ReminderHour < 0 || *f.Params.ReminderHour > 23 {
		errs = append(errs, errors.New("reminder_hour: ожидается час от 0 до 23"))
	}
	s.Location, err = time.LoadLocation(f.Params.Timezone)
	if err != nil {
		errs = append(errs, fmt.Errorf("timezone: %w", err))
	}

	s.Policy = domain.Policy{
		ResponseDays:    s.intRule(domain.RuleResponseDays, &errs),
		SilentDays:      s.intRule(domain.RuleSilentDays, &errs),
		DraftingMaxDays: s.intRule(domain.RuleDraftingMaxDays, &errs),
		SendingMaxDays:  s.intRule(domain.RuleSendingMaxDays, &errs),
		CopiesRequired:  s.intRule(domain.RuleCopiesRequired, &errs),
		CountingNextDay: f.Params.CountingStart == "next_day",
	}
	if r, ok := s.Rules[domain.RuleApplicableFrom]; ok {
		switch v := r.Value.(type) {
		case time.Time:
			s.Policy.ApplicableFrom = civil.New(v.Year(), v.Month(), v.Day())
		case string:
			if d, err := civil.Parse(v); err == nil {
				s.Policy.ApplicableFrom = d
			} else {
				errs = append(errs, fmt.Errorf("%s: %w", domain.RuleApplicableFrom, err))
			}
		default:
			errs = append(errs, fmt.Errorf("%s: value должно быть датой", domain.RuleApplicableFrom))
		}
	}
	for _, id := range []string{domain.RuleServiceDayIsEnd, domain.RuleCountingNextDay, domain.RuleNoRemarksInForm, domain.RuleNewActAfterRefus} {
		if _, ok := s.Rules[id]; !ok {
			errs = append(errs, fmt.Errorf("нет правила %s", id))
		}
	}
	seen := map[string]bool{}
	for _, rm := range f.Reminders {
		if rm.Day < 0 || rm.Day > s.Policy.SilentDays+1 || rm.Kind == "" || seen[rm.Kind] {
			errs = append(errs, fmt.Errorf("напоминание %+v некорректно", rm))
		}
		seen[rm.Kind] = true
	}

	s.Catalog, err = LoadCatalog(filepath.Join(dir, "catalog", "pp290.json"))
	if err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("config/rules: %w", errors.Join(errs...))
	}
	return s, nil
}

func (s *Set) intRule(id string, errs *[]error) int {
	r, ok := s.Rules[id]
	if !ok {
		*errs = append(*errs, fmt.Errorf("нет правила %s", id))
		return 0
	}
	v, ok := r.Value.(int)
	if !ok || v <= 0 {
		*errs = append(*errs, fmt.Errorf("правило %s: value должно быть положительным целым", id))
	}
	return v
}

// ValidateTexts проверяет, что для всех напоминаний и сводки сроков есть шаблоны.
func (s *Set) ValidateTexts(t *Texts) error {
	var errs []error
	for _, r := range s.Reminders {
		if !t.HasMsg(r.Template) {
			errs = append(errs, fmt.Errorf("нет шаблона %s для напоминания %s", r.Template, r.Kind))
		}
	}
	if !t.HasMsg("deadlines_summary") {
		errs = append(errs, errors.New("нет шаблона deadlines_summary"))
	}
	return errors.Join(errs...)
}

// Basis — описание основания для показа пользователю.
type Basis struct {
	RuleID  string      `json:"rule_id"`
	Kind    domain.Kind `json:"kind"`
	Summary string      `json:"summary"`
	Source  *Source     `json:"source"`
	Point   *string     `json:"point"`
}

func (s *Set) Basis(ids ...string) []Basis {
	out := make([]Basis, 0, len(ids))
	for _, id := range ids {
		r, ok := s.Rules[id]
		if !ok {
			continue
		}
		b := Basis{RuleID: id, Kind: r.Kind, Summary: r.Summary, Point: r.Point}
		if src, ok := s.Sources[r.Source]; ok {
			b.Source = &src
		}
		out = append(out, b)
	}
	return out
}

// Cite — ссылка на акт для текста: «порядок, утверждённый приказом …». Пункт указывается, только если сверен.
func (s *Set) Cite(ruleID string) string {
	r, ok := s.Rules[ruleID]
	if !ok {
		return ""
	}
	src, ok := s.Sources[r.Source]
	if !ok {
		return ""
	}
	if r.Point != nil && *r.Point != "" {
		return fmt.Sprintf("п. %s порядка, утверждённого %s", *r.Point, src.Cite)
	}
	return "порядок, утверждённый " + src.Cite
}

// Catalog — справочник работ ПП № 290 для автоподсказки.
type Catalog struct {
	Source  string        `json:"source"`
	Edition string        `json:"edition"`
	Checked string        `json:"checked_at"`
	Note    string        `json:"note"`
	Items   []CatalogItem `json:"items"`
}

type CatalogItem struct {
	Ref     string `json:"ref"`
	Section string `json:"section"`
	Text    string `json:"text"`
	search  string
}

func LoadCatalog(path string) (*Catalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("справочник работ: %w", err)
	}
	var c Catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("справочник работ: %w", err)
	}
	if len(c.Items) == 0 {
		return nil, errors.New("справочник работ пуст")
	}
	for i := range c.Items {
		c.Items[i].search = normalize(c.Items[i].Text + " " + c.Items[i].Section)
	}
	return &c, nil
}

func normalize(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "ё", "е")
}

// Search ищет позиции, в которых встречаются все слова запроса (по началу слова).
func (c *Catalog) Search(q string, limit int) []CatalogItem {
	words := strings.Fields(normalize(q))
	if len(words) == 0 {
		return nil
	}
	var out []CatalogItem
	for _, it := range c.Items {
		ok := true
		for _, w := range words {
			if !strings.Contains(it.search, w) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, it)
			if len(out) == limit {
				break
			}
		}
	}
	return out
}
