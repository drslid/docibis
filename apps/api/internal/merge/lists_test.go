package merge

import (
	"strings"
	"testing"
)

const products = `{"products": [
	{"name": "Pomme", "type": "Fruit", "price": 2},
	{"name": "Carotte", "type": "Légume", "price": 1.5},
	{"name": "Banane", "type": "Fruit", "price": 3},
	{"name": "Poireau", "type": "Légume"}
]}`

func TestFilterSortGroupLikeDocmosis(t *testing.T) {
	cases := []struct{ body, want string }{
		{`<text:p>&lt;&lt;rs_products:filter(type='Fruit')&gt;&gt;&lt;&lt;name&gt;&gt; &lt;&lt;es_products:filter(type='Fruit')&gt;&gt;</text:p>`, `<text:p>Pomme Banane </text:p>`},
		{`<text:p>&lt;&lt;rs_products:sort(name)&gt;&gt;&lt;&lt;name&gt;&gt; &lt;&lt;es_products&gt;&gt;</text:p>`, `<text:p>Banane Carotte Poireau Pomme </text:p>`},
		{`<text:p>&lt;&lt;rs_products:sort(DESC,price)&gt;&gt;&lt;&lt;name&gt;&gt; &lt;&lt;es_products:sort(DESC,price)&gt;&gt;</text:p>`, `<text:p>Banane Pomme Carotte Poireau </text:p>`},
		{`<text:p>&lt;&lt;rs_products:sort(type,DESC,price)&gt;&gt;&lt;&lt;name&gt;&gt; &lt;&lt;es_products&gt;&gt;</text:p>`, `<text:p>Banane Pomme Carotte Poireau </text:p>`},
		{`<text:p>&lt;&lt;rs_products:filter(price&gt;1):sort(name)&gt;&gt;&lt;&lt;$itemNum&gt;&gt;.&lt;&lt;name&gt;&gt; &lt;&lt;es_products&gt;&gt;</text:p>`, `<text:p>1.Banane 2.Carotte 3.Pomme </text:p>`},
		{`<text:p>&lt;&lt;rs_products:filter({price&lt;=2})&gt;&gt;&lt;&lt;name&gt;&gt; &lt;&lt;es_products&gt;&gt;</text:p>`, `<text:p>Pomme Carotte </text:p>`},
		{`<text:p>&lt;&lt;rs_products:group(type)&gt;&gt;</text:p><text:p>&lt;&lt;$groupKey&gt;&gt; :</text:p><text:p>&lt;&lt;rs_$groupItems&gt;&gt;&lt;&lt;name&gt;&gt; &lt;&lt;es_$groupItems&gt;&gt;</text:p><text:p>&lt;&lt;es_products:group(type)&gt;&gt;</text:p>`,
			`<text:p>Fruit :</text:p><text:p>Pomme Banane </text:p><text:p>Légume :</text:p><text:p>Carotte Poireau </text:p>`},
	}
	for _, c := range cases {
		if got := run(t, c.body, products); got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.body, got, c.want)
		}
	}
}

func TestSortedRows(t *testing.T) {
	body := `<table:table><table:table-row><table:table-cell><text:p>&lt;&lt;rr_products:sort(DESC,name)&gt;&gt;&lt;&lt;name&gt;&gt;&lt;&lt;er_products&gt;&gt;</text:p></table:table-cell></table:table-row></table:table>`
	got := run(t, body, products)
	if strings.Count(got, "<table:table-row>") != 4 || strings.Index(got, "Pomme") > strings.Index(got, "Banane") || strings.Contains(got, "er_") {
		t.Fatalf("%s", got)
	}
	grouped := `<table:table><table:table-row><table:table-cell><text:p>&lt;&lt;rr_products:group(type)&gt;&gt;&lt;&lt;$groupKey&gt;&gt; (&lt;&lt;$rownum&gt;&gt;/&lt;&lt;$size&gt;&gt;)&lt;&lt;er_products:group(type)&gt;&gt;</text:p></table:table-cell></table:table-row></table:table>`
	if got := run(t, grouped, products); !strings.Contains(got, "Fruit (1/2)") || !strings.Contains(got, "Légume (2/2)") {
		t.Fatalf("%s", got)
	}
}

func TestListNames(t *testing.T) {
	for name, want := range map[string]string{
		"products":                      "products",
		"products:filter(type='Fruit')": "products",
		"order.lines:sort(DESC,price):filter(qty>0)":  "order.lines",
		"products:other(x)":                           "products:other(x)",
		"products:filter(name='a:b (c)'):group(type)": "products",
	} {
		if got := listPath(name); got != want {
			t.Errorf("%s: %s", name, got)
		}
	}
	fields, err := Fields(odt(t, `<text:p>&lt;&lt;rs_products:group(type)&gt;&gt;&lt;&lt;$groupKey&gt;&gt;&lt;&lt;rs_$groupItems&gt;&gt;&lt;&lt;name&gt;&gt;&lt;&lt;es_$groupItems&gt;&gt;&lt;&lt;es_products&gt;&gt;</text:p>`))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range fields {
		paths = append(paths, f.Path)
	}
	if strings.Join(paths, ",") != "products,products[].name" {
		t.Fatalf("contract: %v", paths)
	}
}
