package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// taggedPDF writes a one-page tagged PDF whose paragraphs carry the given
// tags, with the role map given.
func taggedPDF(roleMap string, tags ...string) []byte {
	content, kids := "", ""
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R /StructTreeRoot 4 0 R /MarkInfo << /Marked true >> >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Contents 6 0 R /StructParents 0 >>",
		"<< /Type /StructTreeRoot /K 5 0 R /RoleMap << " + roleMap + " >> >>",
		"", // the document element, below
		"", // the content, below
	}
	for i, tag := range tags {
		content += fmt.Sprintf("/%s << /MCID %d >> BDC EMC\n", tag, i)
		kids += fmt.Sprintf(" %d 0 R", len(objects)+1)
		objects = append(objects, fmt.Sprintf("<< /Type /StructElem /S /%s /P 5 0 R /Pg 3 0 R /K %d >>", tag, i))
	}
	objects[4] = "<< /Type /StructElem /S /Document /P 4 0 R /K [" + kids + " ] >>"
	objects[5] = fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content)
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	offsets := make([]int, len(objects))
	for i, o := range objects {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return b.Bytes()
}

func TestUnmapStandardTypes(t *testing.T) {
	ctx := context.Background()
	clean := taggedPDF("/Standard /P", "Standard")
	if out, err := unmapStandardTypes(ctx, clean); err != nil || !bytes.Equal(out, clean) {
		t.Fatalf("nothing to change: %v", err)
	}

	pdf := taggedPDF("/Note /P /Standard /P /Note_ /Span", "Note", "Standard", "Note")
	out, err := unmapStandardTypes(ctx, pdf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, pdf) || len(out) == len(pdf) {
		t.Fatal("an incremental update is expected")
	}
	doc, err := pdfapi.ReadAndValidate(ctx, bytes.NewReader(out), model.NewDefaultConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	root, _ := doc.DereferenceDict(types.IndirectRef{ObjectNumber: 4, GenerationNumber: 0})
	roleMap, _ := doc.DereferenceDict(root["RoleMap"])
	if roleMap["Note"] != nil || roleMap["Note__"] != types.Name("P") || roleMap["Note_"] != types.Name("Span") || roleMap["Standard"] != types.Name("P") {
		t.Fatalf("role map: %v", roleMap)
	}
	for nr, want := range map[int]string{7: "Note__", 8: "Standard", 9: "Note__"} {
		elem, _ := doc.DereferenceDict(types.IndirectRef{ObjectNumber: types.Integer(nr)})
		if elem["S"] != types.Name(want) {
			t.Fatalf("element %d: %v, want %s", nr, elem["S"], want)
		}
	}
}
