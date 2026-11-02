package merge

import "testing"

func TestDocmosisFunctions(t *testing.T) {
	data := `{"nom": "jean DUPONT", "vide": " ", "genre": "f", "debut": "2026-01-31", "fin": "2027-03-15", "jour": "15/03/2027", "montant": 1280.5, "liste": "a|b|c"}`
	for _, tc := range []struct{ tag, want string }{
		{`{titleCase(nom)}`, "Jean Dupont"},
		{`{toUpperCase(nom)}`, "JEAN DUPONT"},
		{`{toSentence('bonjour. ça va ? oui')}`, "Bonjour. Ça va ? Oui"},
		{`{left(nom, 4)}`, "jean"},
		{`{right(nom, 6)}`, "DUPONT"},
		{`{split(liste, '|', 1)}`, "b"},
		{`{indexOf(nom, 'DU')}`, "5"},
		{`{replaceFirst('11 plus 11', '11', 'Onze')}`, "Onze plus 11"},
		{`{replaceStr('Bob bob', 'BOB', 'Al', true)}`, "Al Al"},
		{`{countStr('Bob', 'b', true)}`, "2"},
		{`{toRoman(28)}`, "xxviii"},
		{`{toAlpha(28)}`, "bb"},
		{`{toAlpha2(28)}`, "ab"},
		{`{char(169)}`, "©"},
		{`{ceil(2.1)}`, "3"},
		{`{floor(2.9)}`, "2"},
		{`{pow(7, 2)}`, "49"},
		{`{sqrt(81)}`, "9"},
		{`{ifBlank(vide, 'aucun')}`, "aucun"},
		{`{ifBlank(absent, 'aucun')}`, "aucun"},
		{`{ifBlank(nom, 'aucun')}`, "jean DUPONT"},
		{`{isBlank(vide)}`, "true"},
		{`{map(genre, 'M', 'Monsieur', 'F', 'Madame', 'Madame, Monsieur')}`, "Madame, Monsieur"},
		{`{mapi(genre, 'M', 'Monsieur', 'F', 'Madame', 'Madame, Monsieur')}`, "Madame"},
		{`{dateAdd(debut, 1, 'month', 'dd/MM/yyyy')}`, "28/02/2026"},
		{`{dateAdd(debut, -2, 'days', 'dd/MM/yyyy')}`, "29/01/2026"},
		{`{dateAdd(jour, 1, 'year', 'yyyy', 'dd/MM/yyyy')}`, "2028"},
		{`{dateFormat(dateAdd(debut, 1, 'week'), 'dd/MM/yyyy')}`, "07/02/2026"},
		{`{dateDiff(debut, fin, 'months')}`, "13"},
		{`{dateDiff(debut, fin, 'years')}`, "1"},
		{`{dateDiff(debut, fin, 'days')}`, "408"},
		{`{numToText(123)}`, "one hundred and twenty three"},
		{`{numToText(123, 'andNone')}`, "one hundred twenty three"},
		{`{numToText(1005)}`, "one thousand and five"},
		{`{numToDollars(12.33)}`, "twelve dollars and thirty three cents"},
		{`{numToText(71, 'fr')}`, "soixante et onze"},
		{`{numToText(80, 'fr')}`, "quatre-vingts"},
		{`{numToText(81, 'fr')}`, "quatre-vingt-un"},
		{`{numToText(91, 'fr')}`, "quatre-vingt-onze"},
		{`{numToText(200, 'fr')}`, "deux cents"},
		{`{numToText(201, 'fr')}`, "deux cent un"},
		{`{numToText(80000, 'fr')}`, "quatre-vingt mille"},
		{`{numToText(1280.5, 'fr')}`, "mille deux cent quatre-vingts virgule cinq"},
		{`{numToText(2000000, 'fr')}`, "deux millions"},
		{`{numToEuros(montant)}`, "mille deux cent quatre-vingts euros et cinquante centimes"},
		{`{numToEuros(1)}`, "un euro"},
		{`{numToEuros(1000000)}`, "un million d'euros"},
		{`{numToEuros(0.8)}`, "quatre-vingts centimes"},
		{`{numToEuros(21.01, 'en')}`, "twenty one euros and one cent"},
	} {
		got := run(t, `<text:p>&lt;&lt;`+xmlText.Replace(tc.tag)+`&gt;&gt;</text:p>`, data)
		if want := `<text:p>` + xmlText.Replace(tc.want) + `</text:p>`; got != want {
			t.Errorf("%s\n got %s\nwant %s", tc.tag, got, want)
		}
	}
}
