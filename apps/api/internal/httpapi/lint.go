package httpapi

import (
	"encoding/json"
	"strings"

	"docibis/internal/merge"
)

// lintOptions tells the checks what this server offers.
func lintOptions() merge.LintOptions {
	fonts := installedFonts()
	if len(fonts) == 0 {
		return merge.LintOptions{}
	}
	return merge.LintOptions{Font: fontInstalled(fonts)}
}

// fontInstalled tells whether a family is among fonts, by lower-case name.
func fontInstalled(fonts map[string]string) func(string) bool {
	return func(family string) bool {
		_, ok := fonts[strings.ToLower(strings.TrimSpace(family))]
		return ok
	}
}

// lintTemplate checks a template prepared as ODT and returns the checks as
// JSON, never empty: a template that cannot be read has one error.
func lintTemplate(odt []byte) string {
	issues, err := merge.Lint(odt, lintOptions())
	if err != nil {
		issues = []merge.Issue{{Level: "error", Code: "unreadable", Message: "Le modèle est illisible : " + err.Error()}}
	}
	if issues == nil {
		issues = []merge.Issue{}
	}
	raw, _ := json.Marshal(issues)
	return string(raw)
}

func lintIssues(issues []merge.Issue) []LintIssue {
	out := make([]LintIssue, 0, len(issues))
	for _, i := range issues {
		out = append(out, LintIssue{Level: LintIssueLevel(i.Level), Code: i.Code, Message: i.Message})
	}
	return out
}

// lintList reads the checks kept with a version; nil until they are done.
func lintList(raw string) *[]LintIssue {
	var issues []merge.Issue
	if raw == "" || json.Unmarshal([]byte(raw), &issues) != nil {
		return nil
	}
	out := lintIssues(issues)
	return &out
}

// lintSummary counts the checks kept with a version by level.
func lintSummary(raw string) *LintSummary {
	issues := lintList(raw)
	if issues == nil {
		return nil
	}
	var sum LintSummary
	for _, i := range *issues {
		switch i.Level {
		case LintIssueLevelError:
			sum.Errors++
		case LintIssueLevelWarning:
			sum.Warnings++
		default:
			sum.Infos++
		}
	}
	return &sum
}

func fieldList(fields []merge.Field) []TemplateField {
	out := make([]TemplateField, 0, len(fields))
	for _, f := range fields {
		out = append(out, TemplateField{Path: f.Path, Kind: TemplateFieldKind(f.Kind), Optional: f.Optional})
	}
	return out
}
