package httpapi

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

// testFont builds a TrueType font that only has its table of names.
func testFont(family, style string) []byte {
	be := binary.BigEndian
	var records, texts []byte
	for id, s := range map[uint16]string{1: family, 2: style} {
		u := utf16.Encode([]rune(s))
		rec := make([]byte, 12)
		be.PutUint16(rec[0:], 3) // Windows, Unicode, English
		be.PutUint16(rec[2:], 1)
		be.PutUint16(rec[4:], 0x409)
		be.PutUint16(rec[6:], id)
		be.PutUint16(rec[8:], uint16(2*len(u)))
		be.PutUint16(rec[10:], uint16(len(texts)))
		records = append(records, rec...)
		for _, c := range u {
			texts = be.AppendUint16(texts, c)
		}
	}
	table := make([]byte, 6)
	be.PutUint16(table[2:], 2)
	be.PutUint16(table[4:], uint16(6+len(records)))
	table = append(append(table, records...), texts...)
	head := make([]byte, 28)
	be.PutUint32(head[0:], 0x00010000)
	be.PutUint16(head[4:], 1)
	copy(head[12:], "name")
	be.PutUint32(head[20:], uint32(len(head)))
	be.PutUint32(head[24:], uint32(len(table)))
	return append(head, table...)
}

func TestReadFontFaces(t *testing.T) {
	raw := testFont("Marque Sans", "Bold")
	faces, err := readFontFaces(bytes.NewReader(raw), int64(len(raw)))
	if err != nil || len(faces) != 1 || !slices.Equal(faces[0].families, []string{"Marque Sans"}) || faces[0].style != "Bold" {
		t.Fatalf("%+v %v", faces, err)
	}
	// A collection holds several fonts.
	be := binary.BigEndian
	ttc := []byte("ttcf\x00\x02\x00\x00\x00\x00\x00\x02")
	one, two := testFont("Marque", "Regular"), testFont("Marque", "Italic")
	ttc = be.AppendUint32(be.AppendUint32(ttc, 20), uint32(20+len(one)))
	// Each font's tables are found from the start of the file.
	be.PutUint32(one[20:], uint32(20+28))
	be.PutUint32(two[20:], uint32(20+len(one)+28))
	ttc = append(append(ttc, one...), two...)
	f, err := newFont("marque.ttc", ttc)
	if err != nil || f.Family != "Marque" || f.Style != "Regular, Italic" || fontExtension(ttc) != ".ttc" {
		t.Fatalf("%+v %v", f, err)
	}
	for _, bad := range [][]byte{nil, []byte("%PDF-1.7"), raw[:20], append([]byte("OTTO"), raw[4:20]...)} {
		if _, err := newFont("x.ttf", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	// The fonts of the system are read, when there are some.
	if _, err := os.Stat("/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"); err == nil {
		if fonts := scanFonts([]string{"/usr/share/fonts"}); fonts["dejavu sans"] != "DejaVu Sans" {
			t.Errorf("DejaVu Sans not among %d families", len(fonts))
		}
	}
}

func TestFonts(t *testing.T) {
	for _, k := range []string{"FONTCONFIG_FILE", fontsAddedEnv} {
		t.Setenv(k, os.Getenv(k))
	}
	// A font in the home folder: the checks run even where the system has none (CI).
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".fonts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".fonts", "base.ttf"), testFont("Base Test", "Regular"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	forgetInstalledFonts()
	srv, setup := open(t, nil)
	srv.SetRenderer(&fakeRenderer{})
	t.Cleanup(func() {
		if srv.fonts.recheck != nil {
			srv.fonts.recheck.Stop()
		}
		if srv.fonts.dir != "" {
			_ = os.RemoveAll(srv.fonts.dir)
		}
		forgetInstalledFonts()
	})
	h := srv.Handler()
	admin := &client{t: t, h: h}
	admin.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupCode": setup.String(), "username": "admin", "password": "a long enough password"})

	// A template in a font the server lacks.
	content := `<?xml version="1.0" encoding="UTF-8"?><office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:svg="urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0">` +
		`<office:font-face-decls><style:font-face style:name="Marque Test" svg:font-family="'Marque Test'"/></office:font-face-decls>` +
		`<office:automatic-styles><style:style style:name="P1" style:family="paragraph"><style:text-properties style:font-name="Marque Test"/></style:style></office:automatic-styles>` +
		`<office:body><office:text><text:p text:style-name="P1">Bonjour &lt;&lt;nom&gt;&gt;</text:p></office:text></office:body></office:document-content>`
	template := zipOf(t, "mimetype", "application/vnd.oasis.opendocument.text", "content.xml", content)
	if code, out := admin.send(http.MethodPost, "/api/templates", map[string]part{"file": {"lettre.odt", template}}, map[string]string{"path": "marque/lettre.odt"}); code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, out)
	}
	overview := func() map[string]any {
		code, out, _ := admin.do(http.MethodGet, "/api/fonts", nil)
		if code != http.StatusOK {
			t.Fatalf("list: %d %v", code, out)
		}
		return out
	}
	if missing := fmt.Sprint(overview()["missing"]); !strings.Contains(missing, "family:Marque Test") || !strings.Contains(missing, "marque/lettre.odt") {
		t.Fatalf("missing: %s", missing)
	}

	// The administrator adds it: it is installed for the workers.
	font := testFont("Marque Test", "Regular")
	code, added := admin.send(http.MethodPost, "/api/fonts", map[string]part{"file": {"../../marque.ttf", font}}, nil)
	if code != http.StatusCreated || added["family"] != "Marque Test" || added["style"] != "Regular" || added["file"] != "marque.ttf" || added["createdBy"] != "admin" {
		t.Fatalf("add: %d %v", code, added)
	}
	if code, again := admin.send(http.MethodPost, "/api/fonts", map[string]part{"file": {"copie.ttf", font}}, nil); code != http.StatusCreated || again["id"] != added["id"] {
		t.Fatalf("added twice: %d %v", code, again)
	}
	files, _ := os.ReadDir(filepath.Join(srv.fonts.dir, "files"))
	conf, _ := os.ReadFile(os.Getenv("FONTCONFIG_FILE"))
	if len(files) != 1 || !strings.HasSuffix(files[0].Name(), ".ttf") || !strings.Contains(string(conf), filepath.Join(srv.fonts.dir, "files")) ||
		os.Getenv(fontsAddedEnv) != filepath.Join(srv.fonts.dir, "files") {
		t.Fatalf("not installed: %v %s", files, conf)
	}
	out := overview()
	if !strings.Contains(fmt.Sprint(out["installed"]), "Marque Test") || strings.Contains(fmt.Sprint(out["missing"]), "Marque Test") || len(out["added"].([]any)) != 1 {
		t.Fatalf("after: %v", out)
	}

	if code, out := admin.send(http.MethodPost, "/api/fonts", map[string]part{"file": {"x.ttf", []byte("%PDF-1.7")}}, nil); code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["detail"]), "police") {
		t.Fatalf("not a font: %d %v", code, out)
	}
	if code, _, _ := admin.do(http.MethodDelete, fmt.Sprintf("/api/fonts/%v", added["id"]), nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if files, _ := os.ReadDir(filepath.Join(srv.fonts.dir, "files")); len(files) != 0 {
		t.Fatalf("still installed: %v", files)
	}
	if code, _, _ := admin.do(http.MethodDelete, fmt.Sprintf("/api/fonts/%v", added["id"]), nil); code != http.StatusNotFound {
		t.Fatalf("delete again: %d", code)
	}
	if requiredRole(http.MethodPost, "/api/fonts") != "admin" || requiredRole(http.MethodDelete, "/api/fonts/1") != "admin" || requiredRole(http.MethodGet, "/api/fonts") != "" {
		t.Error("only administrators change the fonts")
	}
}
