package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"docibis/internal/store"
	"docibis/internal/version"
)

// The MCP server: streamable HTTP without sessions (any task
// answers any call), behind the API keys of /v1.
const mcpPath = "/v1/mcp"

const mcpInstructions = `DocIbis produit des documents PDF et Word à partir de modèles Word (balises Docmosis <<champ>>) et de données JSON.

Appelez d'abord docibis_whoami. Pour produire un document : docibis_analyze_template sur le modèle (fichier .docx en base64) donne ses balises et des données d'exemple ; complétez-les, puis appelez docibis_render_document. Le document revient en pièce jointe (base64).

docibis_list_documents et docibis_stats renseignent sur les documents déjà produits ; les documents eux-mêmes ne sont pas gardés.

Moteur natif : avec un jeton personnel, docibis_native_context, docibis_native_preview et docibis_native_submit aident à écrire la copie Typst d'une version de modèle, que DocIbis compare à LibreOffice avant de l'utiliser.

Ne produisez un document que si l'utilisateur le demande.`

func readOnly() *mcp.ToolAnnotations {
	no := false
	return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &no}
}

type emptyInput struct{}

type templateInput struct {
	Template string `json:"template_base64" jsonschema:"le modèle Word (.docx) ou OpenDocument (.odt), encodé en base64"`
	FileName string `json:"file_name,omitempty" jsonschema:"nom du fichier du modèle, pour l'historique (modele.docx par défaut)"`
}

type renderInput struct {
	Template     string         `json:"template_base64,omitempty" jsonschema:"le modèle Word (.docx) ou OpenDocument (.odt), encodé en base64 ; ou bien template_name"`
	FileName     string         `json:"file_name,omitempty" jsonschema:"nom du fichier du modèle, pour l'historique (modele.docx par défaut)"`
	TemplateName string         `json:"template_name,omitempty" jsonschema:"chemin d'un modèle de la bibliothèque (voir docibis_list_templates) ; sa version publiée est utilisée"`
	Data         map[string]any `json:"data" jsonschema:"les données JSON du document : un objet dont les clés sont les balises du modèle"`
	Format       string         `json:"format,omitempty" jsonschema:"pdf (par défaut), docx, doc, odt, rtf, html ou txt"`
	Watermark    string         `json:"watermark,omitempty" jsonschema:"filigrane du PDF (texte) ; none pour aucun ; par défaut celui de la configuration"`
	Pdfa         string         `json:"pdfa,omitempty" jsonschema:"none, PDF/A-1b, PDF/A-2b ou PDF/A-3b ; par défaut celui de la configuration"`
	Accessible   *bool          `json:"accessible,omitempty" jsonschema:"PDF balisé pour l'accessibilité (PDF/UA)"`
}

type templateList struct {
	Templates []TemplateContract `json:"templates"`
}

type listInput struct {
	Query  string `json:"query,omitempty" jsonschema:"texte cherché dans le modèle, l'auteur et l'erreur"`
	Status string `json:"status,omitempty" jsonschema:"ok, refused (modèle ou données refusés) ou error (panne)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"nombre de documents, 20 par défaut, 50 au plus"`
}

type whoami struct {
	Key       string     `json:"key"`
	Prefix    string     `json:"prefix"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Instance  string     `json:"instance"`
	Version   string     `json:"version"`
	Ready     bool       `json:"engine_ready"`
}

// toolError is shown to the agent, which can correct its call.
func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}

// mcpKey is the API key of the call, already checked by keyGuard.
func (s *Server) mcpKey(ctx context.Context, req *mcp.CallToolRequest) (store.APIKey, error) {
	if k, ok := keyFrom(ctx); ok {
		return k, nil
	}
	var h http.Header
	if extra := req.GetExtra(); extra != nil {
		h = extra.Header
	}
	secret := strings.TrimSpace(strings.TrimPrefix(h.Get("Authorization"), "Bearer "))
	if secret == "" {
		secret = strings.TrimSpace(h.Get("X-API-Key"))
	}
	k, ok, err := s.activeKey(ctx, secret)
	if err == nil && !ok {
		err = fmt.Errorf("clé API ou jeton personnel absent, inconnu, expiré ou révoqué")
	}
	return k, err
}

// logMCP writes each call of an MCP client in the logs and in the journal of
// the Integrations page: who, which tool, how it went and how long it took.
// Neither the arguments nor the answers are kept.
func (s *Server) logMCP(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		start := time.Now()
		res, err := next(ctx, method, req)
		if strings.HasPrefix(method, "notifications/") || method == "ping" {
			return res, err
		}
		c := &store.MCPCall{Method: method, Status: "ok", Via: "key", DurationMs: ms(time.Since(start))}
		if k, ok := keyFrom(ctx); ok {
			c.Actor, c.UserID = k.Name, k.UserID
			if k.UserID != 0 {
				c.Via = "token"
			}
		}
		if call, ok := req.(*mcp.CallToolRequest); ok && call.Params != nil {
			c.Tool = call.Params.Name
		}
		if err != nil {
			c.Status, c.Error = "error", err.Error()
		} else if r, ok := res.(*mcp.CallToolResult); ok && r != nil && r.IsError {
			c.Status = "error"
			for _, content := range r.Content {
				if t, ok := content.(*mcp.TextContent); ok {
					c.Error = t.Text
					break
				}
			}
		}
		c.Error = shorten(c.Error, 500)
		level := slog.LevelInfo
		if c.Status != "ok" {
			level = slog.LevelWarn
		}
		s.logger.LogAttrs(ctx, level, "mcp call", slog.String("request_id", requestID(ctx)), slog.String("actor", c.Actor),
			slog.String("via", c.Via), slog.String("method", method), slog.String("tool", c.Tool), slog.String("status", c.Status),
			slog.String("error", c.Error), slog.Int("duration_ms", c.DurationMs))
		if st := s.db.Load(); st != nil {
			if err := st.RecordMCPCall(context.WithoutCancel(ctx), c); err != nil {
				s.logger.Warn("MCP call not recorded", "error", err)
			}
		}
		return res, err
	}
}

// shorten cuts a text to n characters at most.
func shorten(text string, n int) string {
	if r := []rune(text); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return text
}

func templateForm(in templateInput) (tryForm, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(in.Template))
	if err != nil || len(raw) == 0 {
		return tryForm{}, refuse("template_base64 : le modèle doit être encodé en base64.")
	}
	name := filepath.Base(strings.TrimSpace(in.FileName))
	if name == "" || name == "." || name == "/" {
		name = "modele.docx"
	}
	return tryForm{name: name, template: raw}, nil
}

func (s *Server) mcpServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "docibis", Title: "DocIbis", Version: version.Version}, &mcp.ServerOptions{Instructions: mcpInstructions})
	server.AddReceivingMiddleware(s.logMCP)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "docibis_whoami",
		Description: "La clé API utilisée, l'instance et la version de DocIbis, et si le moteur de rendu est prêt. À appeler d'abord.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, whoami, error) {
		k, err := s.mcpKey(ctx, req)
		if err != nil {
			return toolError(err), whoami{}, nil
		}
		instance := ""
		if cfg := s.cfg.Load(); cfg != nil {
			instance = cfg.String("instance_name")
		}
		return nil, whoami{Key: k.Name, Prefix: k.Prefix, ExpiresAt: k.ExpiresAt, Instance: instance, Version: version.Version, Ready: s.renderer.Load() != nil}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "docibis_analyze_template",
		Description: "Lit un modèle Word : ses balises (champs, cases, sections, lignes répétées, images), le moteur possible et des données d'exemple à compléter pour docibis_render_document.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in templateInput) (*mcp.CallToolResult, *TryTemplate, error) {
		if _, err := s.mcpKey(ctx, req); err != nil {
			return toolError(err), nil, nil
		}
		f, err := templateForm(in)
		if err != nil {
			return toolError(err), nil, nil
		}
		out, err := s.analyzeForm(ctx, f)
		if err != nil {
			_, p := s.failure(err)
			return toolError(errors.New(trimmed(p.Detail))), nil, nil
		}
		return nil, &out, nil
	})

	no := false
	mcp.AddTool(server, &mcp.Tool{
		Name:        "docibis_render_document",
		Description: "Produit le document d'un modèle Word (fichier en base64, ou nom d'un modèle de la bibliothèque) et de ses données JSON, en PDF (par défaut) ou en Word. Le document revient en pièce jointe base64 ; il apparaît dans l'historique de DocIbis (origine MCP), sans son contenu.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &no, OpenWorldHint: &no},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in renderInput) (*mcp.CallToolResult, any, error) {
		k, err := s.mcpKey(ctx, req)
		if err != nil {
			return toolError(err), nil, nil
		}
		f := tryForm{templateName: strings.TrimSpace(in.TemplateName)}
		if f.templateName == "" || in.Template != "" {
			if f, err = templateForm(templateInput{Template: in.Template, FileName: in.FileName}); err != nil {
				return toolError(err), nil, nil
			}
		}
		f.opts = s.defaultOptions()
		for name, value := range map[string]string{"watermark": in.Watermark, "pdfa": in.Pdfa} {
			if value != "" {
				if _, err := f.opts.set(name, value); err != nil {
					return toolError(err), nil, nil
				}
			}
		}
		if in.Accessible != nil {
			f.opts.accessible, f.opts.accessibleSet = *in.Accessible, true
		}
		if in.Data == nil {
			return toolError(fmt.Errorf("data : les données JSON du document sont obligatoires")), nil, nil
		}
		if f.data, err = json.Marshal(in.Data); err != nil {
			return toolError(err), nil, nil
		}
		f.hasData = true
		start := time.Now()
		doc, err := s.produceForm(ctx, f, strings.ToLower(strings.TrimSpace(in.Format)))
		s.record(ctx, "mcp", k.Name, doc, err, time.Since(start))
		if err != nil {
			_, p := s.failure(err)
			return toolError(errors.New(trimmed(p.Detail))), nil, nil
		}
		name := doc.fileName()
		mime, _, _ := strings.Cut(contentType(doc.format), ";")
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("%s produit : %d octets en %d ms.", name, len(doc.content), ms(time.Since(start)))},
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "docibis://documents/" + name, MIMEType: mime, Blob: doc.content}},
		}}, nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "docibis_list_templates",
		Description: "Les modèles publiés de la bibliothèque : leur nom (template_name de docibis_render_document), les données que chacun attend et des données d'exemple.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listInput) (*mcp.CallToolResult, *templateList, error) {
		if _, err := s.mcpKey(ctx, req); err != nil {
			return toolError(err), nil, nil
		}
		q := strings.TrimSpace(in.Query)
		res, err := s.ListPublishedTemplates(ctx, ListPublishedTemplatesRequestObject{Params: ListPublishedTemplatesParams{Q: &q}})
		if err != nil {
			return nil, nil, err
		}
		return nil, &templateList{Templates: res.(ListPublishedTemplates200JSONResponse)}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "docibis_list_documents",
		Description: "Les derniers documents produits (console, API ou MCP) : date, modèle, auteur ou clé, durée, résultat et erreur. Ni le contenu ni les données ne sont gardés.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listInput) (*mcp.CallToolResult, *RenderPage, error) {
		if _, err := s.mcpKey(ctx, req); err != nil {
			return toolError(err), nil, nil
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 20
		}
		rows, total, err := s.db.Load().Renders(ctx, store.RenderFilter{Text: strings.TrimSpace(in.Query), Status: strings.TrimSpace(in.Status)}, store.Page{Limit: min(limit, 50)})
		if err != nil {
			return nil, nil, err
		}
		out := RenderPage{Items: make([]RenderEntry, 0, len(rows)), Total: total}
		for _, r := range rows {
			out.Items = append(out.Items, renderEntry(r))
		}
		return nil, &out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "docibis_stats",
		Description: "Les chiffres des 30 derniers jours : documents par jour, taux de réussite, durées médiane et p95, modèles et applications les plus utilisés.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, *Dashboard, error) {
		if _, err := s.mcpKey(ctx, req); err != nil {
			return toolError(err), nil, nil
		}
		out, err := s.dashboard(ctx)
		if err != nil {
			return nil, nil, err
		}
		return nil, &out, nil
	})
	s.addNativeTools(server)
	return server
}

func (s *Server) mcpHandler() http.Handler {
	server := s.mcpServer()
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
}

func (s *Server) GetMcpInfo(_ context.Context, _ GetMcpInfoRequestObject) (GetMcpInfoResponseObject, error) {
	return GetMcpInfo200JSONResponse(McpInfo{
		Available: true, ServerName: "docibis", Url: mcpPath, Transport: "streamable-http", AuthHeader: "Authorization", Version: version.Version,
	}), nil
}
