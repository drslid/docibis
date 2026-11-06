package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestSpacesAndQuotas(t *testing.T) {
	srv, c := signedInAdmin(t, "INT")
	h := srv.Handler()
	for path, body := range map[string]string{
		"habitation/fiche.odt": `<text:p>Fiche de &lt;&lt;nom&gt;&gt;</text:p>`,
		"clauses/rgpd.odt":     `<text:p>Vos données restent chez nous.</text:p>`,
		"auto/devis.odt":       `<text:p>Devis de &lt;&lt;nom&gt;&gt;</text:p>`,
	} {
		if code, out := c.send(http.MethodPost, "/api/templates", map[string]part{"file": {"x.odt", odtTemplate(t, body)}}, map[string]string{"path": path}); code != http.StatusCreated {
			t.Fatalf("create %s: %d %v", path, code, out)
		}
	}

	// A space: its templates by the start of their paths.
	code, space, _ := c.do(http.MethodPost, "/api/spaces", map[string]any{"name": "Habitation", "prefixes": []string{"habitation", "/clauses/", ""}})
	if code != http.StatusCreated || fmt.Sprint(space["prefixes"]) != "[habitation/ clauses/]" || space["templates"] != 2.0 {
		t.Fatalf("create space: %d %v", code, space)
	}
	if code, out, _ := c.do(http.MethodPost, "/api/spaces", map[string]any{"name": "habitation"}); code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["detail"]), "déjà") {
		t.Fatalf("same name: %d %v", code, out)
	}
	id := int64(space["id"].(float64))

	// A key kept to the space, 2 documents a day.
	code, created, _ := c.do(http.MethodPost, "/api/keys", map[string]any{"name": "app-habitation", "spaceId": id, "dailyQuota": 2})
	if code != http.StatusCreated {
		t.Fatalf("create key: %d %v", code, created)
	}
	secret := created["secret"].(string)
	render := func(key, name string) (int, string, string) {
		rec := callRenderNamed(h, key, name, `{"nom": "Dupont"}`)
		return rec.Code, rec.Body.String(), rec.Header().Get("Retry-After")
	}
	if code, body, _ := render(secret, "habitation/fiche"); code != http.StatusOK {
		t.Fatalf("template of the space: %d %s", code, body)
	}
	if code, body, _ := render(secret, "auto/devis"); code != http.StatusNotFound || !strings.Contains(body, "pas dans l'espace « Habitation »") {
		t.Fatalf("template of another space: %d %s", code, body)
	}
	listed := c.raw(http.MethodGet, "/v1/templates", "", nil, "Authorization", "Bearer "+secret).Body.String()
	if !strings.Contains(listed, "habitation/fiche.odt") || !strings.Contains(listed, "clauses/rgpd.odt") || strings.Contains(listed, "auto/devis.odt") {
		t.Fatalf("templates of the key: %s", listed)
	}
	if code, body, _ := render(secret, "habitation/fiche"); code != http.StatusOK {
		t.Fatalf("second document: %d %s", code, body)
	}
	if code, body, wait := render(secret, "habitation/fiche"); code != http.StatusTooManyRequests || !strings.Contains(body, "Quota du jour atteint pour la clé « app-habitation »") || wait == "" {
		t.Fatalf("over the quota: %d %s %q", code, body, wait)
	}
	_, keys := c.list("/api/keys")
	if len(keys) != 1 || fmt.Sprint(keys[0]["usage"]) != "map[month:2 today:2]" || keys[0]["spaceId"] != float64(id) || keys[0]["dailyQuota"] != 2.0 {
		t.Fatalf("keys: %v", keys)
	}

	// The quota of the space holds for all its keys.
	if code, out, _ := c.do(http.MethodPut, fmt.Sprintf("/api/spaces/%d", id), map[string]any{"name": "Habitation", "prefixes": []string{"habitation/", "clauses/"}, "dailyQuota": 2}); code != http.StatusOK || out["usage"].(map[string]any)["today"] != 2.0 || out["keys"] != 1.0 {
		t.Fatalf("update space: %d %v", code, out)
	}
	_, other, _ := c.do(http.MethodPost, "/api/keys", map[string]any{"name": "batch-habitation", "spaceId": id})
	if code, body, _ := render(other["secret"].(string), "habitation/fiche"); code != http.StatusTooManyRequests || !strings.Contains(body, "l'espace « Habitation »") {
		t.Fatalf("quota of the space: %d %s", code, body)
	}
	if code, out, _ := c.do(http.MethodPost, "/api/keys", map[string]any{"name": "x", "spaceId": 999}); code != http.StatusBadRequest {
		t.Fatalf("unknown space: %d %v", code, out)
	}

	// A space with active keys stays.
	if code, out, _ := c.do(http.MethodDelete, fmt.Sprintf("/api/spaces/%d", id), nil); code != http.StatusBadRequest {
		t.Fatalf("delete a space in use: %d %v", code, out)
	}
	for _, k := range []map[string]any{created, other} {
		if code, _, _ := c.do(http.MethodDelete, fmt.Sprintf("/api/keys/%v", k["key"].(map[string]any)["id"]), nil); code != http.StatusNoContent {
			t.Fatalf("revoke: %d", code)
		}
	}
	if code, _, _ := c.do(http.MethodDelete, fmt.Sprintf("/api/spaces/%d", id), nil); code != http.StatusNoContent {
		t.Fatalf("delete space: %d", code)
	}
	if requiredRole(http.MethodPost, "/api/spaces") != "admin" || requiredRole(http.MethodGet, "/api/spaces") != "" {
		t.Error("only administrators change the spaces")
	}
}
