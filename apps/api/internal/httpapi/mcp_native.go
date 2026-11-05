package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"docibis/internal/store"
)

// MCP tools for an assistant that writes the Typst copy of a template
// version (native engine): what the version prints (its fields, its test
// data and the pages of LibreOffice), a preview of a copy against
// LibreOffice, and the submission, which DocIbis compares before using it.
// They need a personal token: an editor reads and previews, an administrator
// attaches, as in the console.

const (
	// Pages drawn small: enough for an assistant to see the layout.
	previewDPI   = 50
	previewPages = 4
)

const nativeRules = `Règles d'une copie Typst (fichier main.typ seul) :
- les données arrivent en texte JSON : #let data = json(bytes(sys.inputs.data)) ; la date du document dans sys.inputs.today (AAAA-MM-JJ) et sys.inputs.now (RFC 3339) ;
- aucune importation de paquet (@preview, @local) ni d'autre fichier : DocIbis compile la copie seule, sans réseau ;
- mêmes polices que le modèle Word (celles de l'image), mêmes marges, mêmes sauts de page ;
- pour des données que la copie ne sait pas reproduire : #panic("docibis-decline: raison") ; LibreOffice produit alors ce document ;
- avant de l'utiliser, DocIbis compare chaque jeu de données d'essai avec LibreOffice : même nombre de pages, même texte, pixels dans la tolérance (native_engine_pixel_threshold, en ‰).
Démarche : docibis_native_context (pages de LibreOffice), puis docibis_native_preview jusqu'à un écart faible sur chaque jeu, enfin docibis_native_submit (administrateur).`

type nativeTarget struct {
	TemplateName string `json:"template_name" jsonschema:"chemin du modèle dans la bibliothèque (voir docibis_list_templates)"`
	Version      int    `json:"version,omitempty" jsonschema:"numéro de version ; par défaut la version publiée, sinon la dernière"`
}

type nativeSourceInput struct {
	TemplateName string `json:"template_name" jsonschema:"chemin du modèle dans la bibliothèque (voir docibis_list_templates)"`
	Version      int    `json:"version,omitempty" jsonschema:"numéro de version ; par défaut la version publiée, sinon la dernière"`
	Source       string `json:"typst_source" jsonschema:"la copie Typst complète (main.typ)"`
	TestData     int    `json:"test_data_index,omitempty" jsonschema:"jeu de données d'essai comparé (0 par défaut), voir test_data de docibis_native_context"`
}

type nativeContextOut struct {
	Template string        `json:"template"`
	Version  int           `json:"version"`
	Native   *NativeStatus `json:"native,omitempty"`
	Source   string        `json:"typst_source,omitempty"`
	Fields   any           `json:"fields,omitempty"`
	TestData []any         `json:"test_data"`
	Pages    int           `json:"libreoffice_pages"`
	Note     string        `json:"note,omitempty"`
	Rules    string        `json:"rules"`
}

type nativePreviewOut struct {
	TestData         int       `json:"test_data_index"`
	Pages            int       `json:"pages"`
	LibreOfficePages int       `json:"libreoffice_pages"`
	Permille         []float64 `json:"permille_per_page,omitempty"`
	Threshold        int       `json:"threshold_permille"`
	Text             string    `json:"text"`
	Note             string    `json:"note,omitempty"`
}

// mcpUser is the account of the personal token of a call, with role at
// least.
func (s *Server) mcpUser(ctx context.Context, req *mcp.CallToolRequest, role string) (store.User, error) {
	k, err := s.mcpKey(ctx, req)
	if err != nil {
		return store.User{}, err
	}
	if k.UserID == 0 {
		return store.User{}, errors.New("cet outil demande un jeton personnel (dit_…, page Profil) : une clé d'application ne touche pas à la bibliothèque")
	}
	u, ok, err := s.db.Load().UserByID(ctx, k.UserID)
	if err != nil {
		return u, err
	}
	if !ok || !u.Active || rank[u.Role] < rank[role] {
		return u, fmt.Errorf("cet outil demande le rôle %s", map[string]string{"editor": "éditeur", "admin": "administrateur"}[role])
	}
	return u, nil
}

// nativeTarget finds the version an assistant names.
func (s *Server) nativeTarget(ctx context.Context, name string, number int) (store.Template, store.TemplateVersion, error) {
	p, err := cleanPath(name)
	if err != nil {
		return store.Template{}, store.TemplateVersion{}, err
	}
	st := s.db.Load()
	candidates := []string{p}
	if path.Ext(p) == "" {
		candidates = append(candidates, p+".docx", p+".odt")
	}
	for _, c := range candidates {
		t, ok, err := st.TemplateByName(ctx, c)
		if err != nil || !ok {
			if err != nil {
				return t, store.TemplateVersion{}, err
			}
			continue
		}
		if t.Kind != "template" {
			return t, store.TemplateVersion{}, refuse("%q est un PDF fixe : il n'a pas de copie Typst.", t.Path)
		}
		if number == 0 {
			number = t.Published
		}
		if number == 0 {
			number = t.Versions
		}
		v, ok, err := st.ServedVersion(ctx, t.ID, number)
		if err == nil && !ok {
			err = refuse("%q n'a pas de version %d.", t.Path, number)
		}
		return t, v, err
	}
	return store.Template{}, store.TemplateVersion{}, refuse("Modèle inconnu dans la bibliothèque : %q.", p)
}

// standardPDF is the document of LibreOffice for one set of test data, as the
// comparison makes it.
func (s *Server) standardPDF(ctx context.Context, t store.Template, v store.TemplateVersion, data []byte) ([]byte, error) {
	doc, err := s.produceForm(ctx, tryForm{
		name: path.Base(t.Path), template: v.Content, prepared: v.Prepared, data: data, hasData: true, format: "pdf",
		opts: renderOptions{pdfa: "none", dataCheck: "off"},
	}, "pdf")
	return doc.content, err
}

// pageImages draws the first pages of a PDF small, for the assistant.
func pageImages(ctx context.Context, pdf []byte, dir, name string) ([]mcp.Content, error) {
	pages, err := rasterizeAt(ctx, pdf, dir, name, previewDPI, previewPages)
	if err != nil {
		return nil, err
	}
	out := make([]mcp.Content, 0, len(pages))
	for _, p := range pages {
		var b bytes.Buffer
		if err := png.Encode(&b, p); err != nil {
			return nil, err
		}
		out = append(out, &mcp.ImageContent{Data: b.Bytes(), MIMEType: "image/png"})
	}
	return out, nil
}

// withText puts the answer first in text, for the clients that do not read
// the structured content, before the images.
func withText(out any, images []mcp.Content) *mcp.CallToolResult {
	raw, _ := json.Marshal(out)
	return &mcp.CallToolResult{Content: append([]mcp.Content{&mcp.TextContent{Text: string(raw)}}, images...)}
}

func (s *Server) addNativeTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "docibis_native_context",
		Description: "Pour écrire la copie Typst d'une version de modèle (moteur natif) : ses champs, ses jeux de données d'essai, sa copie actuelle et son état, " +
			"les règles d'une copie, et les pages produites par LibreOffice pour le premier jeu (images). Jeton personnel d'un éditeur.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in nativeTarget) (*mcp.CallToolResult, *nativeContextOut, error) {
		if _, err := s.mcpUser(ctx, req, "editor"); err != nil {
			return toolError(err), nil, nil
		}
		t, v, err := s.nativeTarget(ctx, in.TemplateName, in.Version)
		if err != nil {
			_, p := s.failure(err)
			return toolError(errors.New(trimmed(p.Detail))), nil, nil
		}
		out := &nativeContextOut{Template: t.Path, Version: v.Number, Source: v.NativeSource, Rules: nativeRules, TestData: []any{}}
		if v.NativeSHA256 != "" {
			out.Native = nativeStatus(v.Native, v.NativeSHA256, len(v.NativeSource), v.NativeBy, v.NativeAt)
		}
		if v.Fields != "" {
			_ = json.Unmarshal([]byte(v.Fields), &out.Fields)
		}
		sets := nativeData(v)
		for _, d := range sets {
			var set any
			if json.Unmarshal(d, &set) == nil {
				out.TestData = append(out.TestData, set)
			}
		}
		var images []mcp.Content
		if len(sets) > 0 {
			images, out.Pages, out.Note = s.standardImages(ctx, t, v, sets[0])
		}
		return withText(out, images), out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "docibis_native_preview",
		Description: "Compile une copie Typst avec un jeu de données d'essai et la compare à LibreOffice, comme le fera DocIbis : pages, texte, écart en ‰ par page, " +
			"et les pages de la copie (images). Rien n'est enregistré. Jeton personnel d'un éditeur.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in nativeSourceInput) (*mcp.CallToolResult, *nativePreviewOut, error) {
		if _, err := s.mcpUser(ctx, req, "editor"); err != nil {
			return toolError(err), nil, nil
		}
		t, v, err := s.nativeTarget(ctx, in.TemplateName, in.Version)
		if err == nil {
			err = checkNativeSource([]byte(in.Source))
		}
		if err != nil {
			_, p := s.failure(err)
			return toolError(errors.New(trimmed(p.Detail))), nil, nil
		}
		out, images, err := s.previewNative(ctx, t, v, in.Source, in.TestData)
		if err != nil {
			return toolError(err), nil, nil
		}
		return withText(out, images), out, nil
	})

	no := false
	mcp.AddTool(server, &mcp.Tool{
		Name: "docibis_native_submit",
		Description: "Joint une copie Typst à une version de modèle ; DocIbis la compare aussitôt à LibreOffice sur tous les jeux d'essai et ne l'utilise que si elle est qualifiée. " +
			"Une nouvelle version Word repasse par LibreOffice. Jeton personnel d'un administrateur.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &no, OpenWorldHint: &no},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in nativeSourceInput) (*mcp.CallToolResult, *NativeStatus, error) {
		u, err := s.mcpUser(ctx, req, "admin")
		if err != nil {
			return toolError(err), nil, nil
		}
		t, v, err := s.nativeTarget(ctx, in.TemplateName, in.Version)
		if err == nil {
			err = checkNativeSource([]byte(in.Source))
		}
		if err != nil {
			_, p := s.failure(err)
			return toolError(errors.New(trimmed(p.Detail))), nil, nil
		}
		st, sha := s.db.Load(), hashText(in.Source)
		if err := st.SetNativeSource(ctx, v.ID, in.Source, sha, u.Username); err != nil {
			return nil, nil, err
		}
		now := time.Now().UTC()
		v.NativeSource, v.NativeSHA256, v.NativeBy, v.NativeAt, v.Native = in.Source, sha, u.Username, &now, ""
		s.audit(ctx, u.Username, "template.native", "template:"+t.Path, fmt.Sprintf("version %d : copie Typst %s jointe (MCP)", v.Number, sha[:12]))
		status, err := s.checkNativeVersion(ctx, t, v)
		if err != nil {
			if code, _ := s.failure(err); code == http.StatusServiceUnavailable {
				// Compared in the background once the engine is free.
				s.describeLater(context.WithoutCancel(ctx))
				return nil, nativeStatus("", sha, len(in.Source), u.Username, &now), nil
			}
			return nil, nil, err
		}
		if err := st.Sync(ctx); err != nil {
			s.logger.Warn("database copy not updated", "error", err)
		}
		return nil, &status, nil
	})
}

// standardImages draws the pages of LibreOffice for one set of data; a note
// tells why when they cannot be drawn.
func (s *Server) standardImages(ctx context.Context, t store.Template, v store.TemplateVersion, data []byte) ([]mcp.Content, int, string) {
	if s.renderer.Load() == nil || len(v.Prepared) == 0 {
		return nil, 0, "Pages de LibreOffice indisponibles : le moteur démarre."
	}
	pdf, err := s.standardPDF(ctx, t, v, data)
	if err != nil {
		_, p := s.failure(err)
		return nil, 0, "LibreOffice ne produit pas ce jeu de données : " + trimmed(p.Detail)
	}
	dir, err := os.MkdirTemp("", "docibis-mcp-")
	if err != nil {
		return nil, 0, err.Error()
	}
	defer os.RemoveAll(dir)
	images, err := pageImages(ctx, pdf, dir, "standard")
	if err != nil {
		return nil, 0, "Pages non dessinées : " + err.Error()
	}
	return images, len(images), ""
}

// previewNative compiles a copy for one set of test data and compares it with
// LibreOffice, without keeping it.
func (s *Server) previewNative(ctx context.Context, t store.Template, v store.TemplateVersion, source string, index int) (*nativePreviewOut, []mcp.Content, error) {
	box := s.native.Load()
	if box == nil {
		return nil, nil, errors.New("le moteur natif n'est pas démarré")
	}
	if s.renderer.Load() == nil || len(v.Prepared) == 0 {
		return nil, nil, errors.New("le moteur de rendu démarre : réessayez dans un instant")
	}
	sets := nativeData(v)
	if index < 0 || index >= len(sets) {
		return nil, nil, fmt.Errorf("test_data_index : de 0 à %d", len(sets)-1)
	}
	file, err := s.nativeFile(ctx, v.ID, hashText(source), source)
	if err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp("", "docibis-mcp-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	out := &nativePreviewOut{TestData: index, Threshold: s.cfg.Load().Int("native_engine_pixel_threshold")}
	drawnFile := filepath.Join(dir, "native.pdf")
	if _, err := box.Compile(ctx, file, drawnFile, nativeInputs(sets[index], time.Now())); err != nil {
		if declines(err) {
			out.Text = "La copie renvoie ce jeu de données à LibreOffice (docibis-decline)."
			return out, nil, nil
		}
		return nil, nil, fmt.Errorf("la copie ne se compile pas : %s", shorten(err.Error(), 2000))
	}
	drawn, err := os.ReadFile(drawnFile)
	if err != nil {
		return nil, nil, err
	}
	standard, err := s.standardPDF(ctx, t, v, sets[index])
	if err != nil {
		_, p := s.failure(err)
		return nil, nil, errors.New("LibreOffice ne produit pas ce jeu de données : " + trimmed(p.Detail))
	}
	images, err := pageImages(ctx, drawn, dir, "preview")
	if err != nil {
		out.Note = "Pages non dessinées : " + err.Error()
		return out, nil, nil
	}
	out.Pages = len(images)
	got, err := rasterize(ctx, drawn, dir, "native")
	if err == nil {
		var want []*image.Gray
		if want, err = rasterize(ctx, standard, dir, "standard"); err == nil {
			out.Pages, out.LibreOfficePages = len(got), len(want)
			if len(got) == len(want) {
				for p := range got {
					out.Permille = append(out.Permille, math.Round(diffPermille(got[p], want[p])*100)/100)
				}
			}
		}
	}
	if err != nil {
		out.Note = "Comparaison impossible : " + err.Error()
		return out, images, nil
	}
	switch where, differ, err := pdfTextDiffers(ctx, standard, drawn, dir, index); {
	case err != nil:
		out.Text = "Texte illisible : " + err.Error()
	case differ:
		out.Text = "Le texte diffère de celui de LibreOffice : " + where
	default:
		out.Text = "Même texte que LibreOffice."
	}
	return out, images, nil
}
