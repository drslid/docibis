package httpapi

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"slices"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// An accessible PDF (PDF/UA) made by LibreOffice tags each paragraph with the
// name of its style, mapped to a standard type in the RoleMap. A style named
// like a standard type (Note, Index, Caption…) remaps that type, which
// PDF/UA forbids: such tags are renamed (Note becomes Note_), in an
// incremental update that leaves the rest of the file as it was (PDF/A).

// standardTypes are the structure types of ISO 32000-1, 14.8.4.
var standardTypes = map[string]bool{}

func init() {
	for _, t := range []string{
		"Document", "Part", "Art", "Sect", "Div", "BlockQuote", "Caption", "TOC", "TOCI", "Index", "NonStruct", "Private",
		"P", "H", "H1", "H2", "H3", "H4", "H5", "H6", "L", "LI", "Lbl", "LBody",
		"Table", "TR", "TH", "TD", "THead", "TBody", "TFoot",
		"Span", "Quote", "Note", "Reference", "BibEntry", "Code", "Link", "Annot",
		"Ruby", "RB", "RT", "RP", "Warichu", "WT", "WP", "Figure", "Formula", "Form",
	} {
		standardTypes[t] = true
	}
}

// unmapStandardTypes renames the tags that remap a standard type; the PDF is
// returned as is when none does.
func unmapStandardTypes(ctx context.Context, pdf []byte) ([]byte, error) {
	if !bytes.Contains(pdf, []byte("/RoleMap")) {
		return pdf, nil
	}
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	conf.WriteObjectStream, conf.WriteXRefStream = false, false
	doc, err := pdfapi.ReadAndValidate(ctx, bytes.NewReader(pdf), conf)
	if err != nil {
		return nil, err
	}
	xref := doc.XRefTable
	catalog, err := xref.Catalog()
	if err != nil {
		return nil, err
	}
	rootRef, ok := catalog["StructTreeRoot"].(types.IndirectRef)
	if !ok {
		return pdf, nil
	}
	root, err := xref.DereferenceDict(rootRef)
	if err != nil || root == nil {
		return pdf, err
	}
	roleMap, err := xref.DereferenceDict(root["RoleMap"])
	if err != nil || roleMap == nil {
		return pdf, err
	}
	renamed := map[string]string{}
	for name := range roleMap {
		if !standardTypes[name] {
			continue
		}
		alias := name + "_"
		for roleMap[alias] != nil || standardTypes[alias] {
			alias += "_"
		}
		renamed[name] = alias
	}
	if len(renamed) == 0 {
		return pdf, nil
	}
	changed := map[int]bool{}
	for name, alias := range renamed {
		roleMap[alias] = roleMap[name]
		delete(roleMap, name)
	}
	owner := rootRef.ObjectNumber.Value()
	if ref, ok := root["RoleMap"].(types.IndirectRef); ok {
		owner = ref.ObjectNumber.Value()
	}
	changed[owner] = true

	// The structure elements, from the root down their K entries; owner is
	// the indirect object that holds the current one.
	seen := map[int]bool{}
	var walk func(o types.Object, owner int) error
	walk = func(o types.Object, owner int) error {
		switch v := o.(type) {
		case types.IndirectRef:
			nr := v.ObjectNumber.Value()
			if seen[nr] {
				return nil
			}
			seen[nr] = true
			target, err := xref.Dereference(v)
			if err != nil {
				return err
			}
			return walk(target, nr)
		case types.Dict:
			if s, ok := v["S"].(types.Name); ok && renamed[string(s)] != "" {
				v["S"] = types.Name(renamed[string(s)])
				changed[owner] = true
			}
			if k, ok := v["K"]; ok {
				return walk(k, owner)
			}
		case types.Array:
			for _, e := range v {
				if err := walk(e, owner); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root["K"], rootRef.ObjectNumber.Value()); err != nil {
		return nil, err
	}
	if doc.Read.FileSize != int64(len(pdf)) {
		return nil, errors.New("unexpected PDF size")
	}
	doc.Write.Increment = true
	doc.Write.Offset = doc.Read.FileSize
	nrs := slices.Sorted(maps.Keys(changed))
	for _, nr := range nrs {
		doc.Write.IncrementWithObjNr(nr)
	}
	out := bytes.NewBuffer(append(make([]byte, 0, len(pdf)+4096), pdf...))
	if err := pdfapi.WriteIncrement(ctx, doc, out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
