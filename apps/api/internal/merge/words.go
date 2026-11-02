package merge

import (
	"math"
	"strconv"
	"strings"
)

// numberInWords writes a number in words: in English as Docmosis does
// ("one hundred and twenty three"; mode andLast, andAlways or andNone), or
// in French with mode fr ("cent vingt-trois").
func numberInWords(f float64, mode string) string {
	lang, and := "en", "andLast"
	switch m := strings.ToLower(strings.TrimSpace(mode)); {
	case strings.HasPrefix(m, "fr"):
		lang = "fr"
	case m == "andalways":
		and = "andAlways"
	case m == "andnone":
		and = "andNone"
	}
	var b strings.Builder
	if f < 0 {
		b.WriteString(map[string]string{"en": "minus ", "fr": "moins "}[lang])
		f = -f
	}
	whole, frac := splitNumber(f)
	b.WriteString(integerInWords(whole, lang, and, true))
	if frac != "" {
		if lang == "fr" {
			b.WriteString(" virgule")
			for len(frac) > 1 && frac[0] == '0' {
				b.WriteString(" zéro")
				frac = frac[1:]
			}
			n, _ := strconv.ParseUint(frac, 10, 64)
			b.WriteString(" " + integerInWords(n, "fr", and, true))
		} else {
			b.WriteString(" point")
			for _, d := range frac {
				b.WriteString(" " + englishUnits[d-'0'])
			}
		}
	}
	return b.String()
}

// amountInWords writes an amount with its currency and cents: "cent euros
// et cinquante centimes", "twelve dollars and thirty three cents".
func amountInWords(f float64, lang, currency string) string {
	minus := ""
	if f < 0 {
		minus = map[string]string{"en": "minus ", "fr": "moins "}[lang]
		f = -f
	}
	cents := uint64(math.Round(f * 100))
	whole, part := cents/100, cents%100
	var out []string
	if whole > 0 || part == 0 {
		words := integerInWords(whole, lang, "andLast", true)
		unit := currency
		if whole > 1 {
			unit += "s"
		}
		if lang == "fr" && (strings.HasSuffix(words, "million") || strings.HasSuffix(words, "millions") ||
			strings.HasSuffix(words, "milliard") || strings.HasSuffix(words, "milliards")) {
			unit = "d'" + currency + "s"
			out = append(out, words+" "+unit)
		} else {
			out = append(out, words+" "+unit)
		}
	}
	if part > 0 {
		name := map[string]string{"en": "cent", "fr": "centime"}[lang]
		if part > 1 {
			name += "s"
		}
		out = append(out, integerInWords(part, lang, "andNone", true)+" "+name)
	}
	sep := map[string]string{"en": " and ", "fr": " et "}[lang]
	return minus + strings.Join(out, sep)
}

// splitNumber returns the whole part and the decimals as written (no
// trailing zero), at most 9 of them.
func splitNumber(f float64) (uint64, string) {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	whole, frac, _ := strings.Cut(s, ".")
	n, _ := strconv.ParseUint(whole, 10, 64)
	if len(frac) > 9 {
		frac = strings.TrimRight(frac[:9], "0")
	}
	return n, frac
}

var englishUnits = []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten",
	"eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen"}
var englishTens = []string{"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"}

var frenchUnits = []string{"zéro", "un", "deux", "trois", "quatre", "cinq", "six", "sept", "huit", "neuf", "dix",
	"onze", "douze", "treize", "quatorze", "quinze", "seize", "dix-sept", "dix-huit", "dix-neuf"}
var frenchTens = []string{"", "", "vingt", "trente", "quarante", "cinquante", "soixante"}

type scale struct {
	value       uint64
	en, fr, frs string
}

var scales = []scale{{1_000_000_000_000, "trillion", "billion", "billions"}, {1_000_000_000, "billion", "milliard", "milliards"},
	{1_000_000, "million", "million", "millions"}, {1_000, "thousand", "mille", "mille"}}

// integerInWords writes a whole number; last tells that nothing follows,
// for the s of the French "quatre-vingts" and "deux cents".
func integerInWords(n uint64, lang, and string, last bool) string {
	if n == 0 {
		if lang == "fr" {
			return "zéro"
		}
		return "zero"
	}
	var parts []string
	rest := n
	for _, sc := range scales {
		if rest < sc.value {
			continue
		}
		k := rest / sc.value
		rest %= sc.value
		if lang == "fr" {
			switch {
			case sc.value == 1_000 && k == 1:
				parts = append(parts, "mille")
			case sc.value == 1_000:
				parts = append(parts, frenchBelow1000(k, false)+" mille")
			case k == 1:
				parts = append(parts, "un "+sc.fr)
			default:
				parts = append(parts, frenchBelow1000(k, true)+" "+sc.frs)
			}
		} else {
			parts = append(parts, englishBelow1000(k, and == "andAlways")+" "+sc.en)
		}
	}
	if rest > 0 {
		if lang == "fr" {
			parts = append(parts, frenchBelow1000(rest, last))
		} else {
			words := englishBelow1000(rest, and != "andNone")
			if and == "andLast" && rest < 100 && len(parts) > 0 {
				words = "and " + words
			}
			parts = append(parts, words)
		}
	}
	return strings.Join(parts, " ")
}

func englishBelow1000(n uint64, and bool) string {
	var parts []string
	if h := n / 100; h > 0 {
		parts = append(parts, englishUnits[h]+" hundred")
		if n%100 > 0 && and {
			parts = append(parts, "and")
		}
	}
	if r := n % 100; r > 0 {
		if r < 20 {
			parts = append(parts, englishUnits[r])
		} else if r%10 == 0 {
			parts = append(parts, englishTens[r/10])
		} else {
			parts = append(parts, englishTens[r/10]+" "+englishUnits[r%10])
		}
	}
	return strings.Join(parts, " ")
}

// frenchBelow1000 follows the traditional spelling: vingt et un, soixante et
// onze, quatre-vingts, quatre-vingt-un, deux cents, deux cent un.
func frenchBelow1000(n uint64, last bool) string {
	h, r := n/100, n%100
	var head string
	switch {
	case h == 1:
		head = "cent"
	case h > 1:
		head = frenchUnits[h] + " cent"
		if r == 0 && last {
			head += "s"
		}
	}
	if r == 0 {
		return head
	}
	tail := frenchBelow100(r, last)
	if head == "" {
		return tail
	}
	return head + " " + tail
}

func frenchBelow100(n uint64, last bool) string {
	if n < 20 {
		return frenchUnits[n]
	}
	t, u := n/10, n%10
	switch t {
	case 7:
		if u == 1 {
			return "soixante et onze"
		}
		return "soixante-" + frenchUnits[10+u]
	case 8:
		if u == 0 {
			if last {
				return "quatre-vingts"
			}
			return "quatre-vingt"
		}
		return "quatre-vingt-" + frenchUnits[u]
	case 9:
		return "quatre-vingt-" + frenchUnits[10+u]
	}
	switch u {
	case 0:
		return frenchTens[t]
	case 1:
		return frenchTens[t] + " et un"
	}
	return frenchTens[t] + "-" + frenchUnits[u]
}
