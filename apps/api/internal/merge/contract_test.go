package merge

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFieldsAndMissing(t *testing.T) {
	body := `<text:p>&lt;&lt;client.nom&gt;&gt; né le &lt;&lt;client.naissance{dateFormat='dd/MM/yyyy'}&gt;&gt;</text:p>` +
		`<text:p>&lt;&lt;cs_{option=true}&gt;&gt;Option : &lt;&lt;option_libelle&gt;&gt;&lt;&lt;es_{option=true}&gt;&gt;</text:p>` +
		`<table:table><table:table-row><table:table-cell><text:p>&lt;&lt;rr_lignes&gt;&gt;&lt;&lt;libelle&gt;&gt; &lt;&lt;produit&gt;&gt;&lt;&lt;er_lignes&gt;&gt;</text:p></table:table-cell></table:table-row></table:table>` +
		`<text:p>&lt;&lt;accord{renderer=boolean(true)}&gt;&gt;</text:p>`
	fields, err := Fields(odt(t, body))
	if err != nil {
		t.Fatal(err)
	}
	want := []Field{
		{Path: "client.nom", Kind: "text"},
		{Path: "client.naissance", Kind: "text"},
		{Path: "option", Kind: "flag", Optional: true},
		{Path: "option_libelle", Kind: "text", Optional: true},
		{Path: "lignes", Kind: "list"},
		{Path: "lignes[].libelle", Kind: "text"},
		{Path: "lignes[].produit", Kind: "text"},
		{Path: "accord", Kind: "flag", Optional: true},
	}
	if !reflect.DeepEqual(fields, want) {
		got, _ := json.Marshal(fields)
		t.Fatalf("fields: %s", got)
	}

	var data map[string]any
	_ = json.Unmarshal([]byte(`{"client": {"nom": "Dupont"}, "produit": "Obsèques", "lignes": [{"libelle": "A"}, {"x": 1}]}`), &data)
	if got := Missing(fields, data); !reflect.DeepEqual(got, []string{"client.naissance", "lignes[].libelle"}) {
		t.Fatalf("missing: %v", got)
	}
	_ = json.Unmarshal([]byte(`{"client": {"nom": "D", "naissance": "1950-01-02"}, "lignes": []}`), &data)
	if got := Missing(fields, data); len(got) != 0 {
		t.Fatalf("an empty list has no row to fill: %v", got)
	}
}
