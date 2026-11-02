package httpapi

import (
	"bytes"
	"strings"
	"testing"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func TestProtectionOptions(t *testing.T) {
	o := renderOptions{pdfa: "none"}
	for name, value := range map[string]string{
		"passwordProtect": "ouvrir", "pdfRestrictPassword": "proprio", "pdfRestrictPrinting": "1",
		"pdfRestrictCopy": "y", "pdfSkipEmptyPages": "true", "title": "Contrat", "author": "ACME",
	} {
		if _, err := o.set(name, value); err != nil {
			t.Fatal(name, err)
		}
	}
	if err := o.resolve(); err != nil {
		t.Fatal(err)
	}
	filter := o.filter("pdf")
	if !strings.Contains(filter, `"IsSkipEmptyPages":{"type":"boolean","value":"true"}`) || strings.Contains(filter, "ouvrir") || strings.Contains(filter, "proprio") {
		t.Errorf("filter %s", filter)
	}
	// AES-256, the opening password needed, the restrictions kept.
	pdf, err := o.protect(t.Context(), minimalPDF(1))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(pdf, []byte("/AESV3")) {
		t.Errorf("not AES-256: %.300s", pdf)
	}
	if _, err := pdfapi.ReadContext(t.Context(), bytes.NewReader(pdf), model.NewDefaultConfiguration()); err == nil {
		t.Error("opened without its password")
	}
	conf := model.NewDefaultConfiguration()
	conf.UserPW = "ouvrir"
	if _, err := pdfapi.ReadContext(t.Context(), bytes.NewReader(pdf), conf); err != nil {
		t.Errorf("not opened with its password: %v", err)
	}
	if f := o.restrict.flags(); f&model.PermissionPrintRev3 != 0 || f&model.PermissionPrintRev2 == 0 || f&model.PermissionExtract != 0 {
		t.Errorf("permissions %b", f)
	}
	// Passwords never reach the history.
	if d := o.describe(); strings.Contains(d, "ouvrir") || strings.Contains(d, "proprio") || !strings.Contains(d, `"protected":"true"`) || !strings.Contains(d, `"title":"Contrat"`) {
		t.Errorf("describe: %s", d)
	}

	for _, tc := range []map[string]string{
		{"pdfRestrictCopy": "true"},                  // restriction without its password
		{"pdfa": "PDF/A-2b", "passwordProtect": "x"}, // PDF/A cannot be encrypted
		{"pdfRestrictPrinting": "3", "pdfRestrictPassword": "p"},
	} {
		o := renderOptions{pdfa: "none"}
		var err error
		for name, value := range tc {
			if _, e := o.set(name, value); e != nil {
				err = e
			}
		}
		if err == nil {
			err = o.resolve()
		}
		if err == nil {
			t.Errorf("%v accepted", tc)
		}
	}
}
