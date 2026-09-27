package api

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"priemka/internal/apicheck"
)

// TestDataAPI прогоняет DATA-API.yaml против настоящего обработчика дважды подряд (жюри может повторять проверки),
// каждый обмен сверяется с openapi.yaml, а ссылки response.schema — с компонентами спецификации.
func TestDataAPI(t *testing.T) {
	c := newContract(t)
	spec, err := apicheck.Load(filepath.Join("..", "..", "..", "DATA-API.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	schemas := c.doc.Components.Schemas
	for _, ch := range spec.Checks {
		if ref := ch.Response.Schema; ref != "" {
			if _, ok := schemas[strings.TrimPrefix(ref, "#/components/schemas/")]; !ok || !strings.HasPrefix(ref, "#/components/schemas/") {
				t.Errorf("%s: схемы %s нет в openapi.yaml", ch.ID, ref)
			}
		}
	}

	opts := apicheck.Options{
		BaseURL: c.srv.URL + "/api/v1",
		Tokens:  map[string]string{"chairman": chairmanToken, "resident": residentToken},
		Observe: func(_ apicheck.Check, req *http.Request, body []byte, resp *http.Response, respBody []byte) {
			c.verify(req, body, resp.StatusCode, resp.Header, respBody)
		},
	}
	for run := 1; run <= 2; run++ {
		for _, r := range spec.Run(context.Background(), opts) {
			if r.Err != nil {
				t.Errorf("прогон %d, %s: %v", run, r.ID, r.Err)
			}
		}
	}
}
