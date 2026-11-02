package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// The company logo shown under DocIbis: none (the default), or one sent by an
// administrator, kept in the settings table.
const (
	brandingKey = "branding"
	logoKey     = "branding.logo"
	maxLogo     = 512 << 10
)

type brandingState struct {
	CompanyLogo string `json:"companyLogo"`
	// The last logo sent stays known, to switch back to it without sending it again.
	ContentType string `json:"contentType,omitempty"`
	Version     string `json:"version,omitempty"`
}

// Anything that could run or fetch something from an SVG.
var svgActive = regexp.MustCompile(`(?i)<script|<foreignobject|<iframe|<embed|<object|<!entity|javascript:|\son[a-z]+\s*=|href\s*=\s*["']?\s*(https?:|data:|//)`)

func (s *Server) branding(ctx context.Context) (brandingState, error) {
	b := brandingState{CompanyLogo: string(BrandingCompanyLogoNone)}
	raw, ok, err := s.db.Load().Setting(ctx, brandingKey)
	if err != nil || !ok {
		return b, err
	}
	if json.Unmarshal([]byte(raw), &b) != nil || !BrandingCompanyLogo(b.CompanyLogo).Valid() {
		b.CompanyLogo = string(BrandingCompanyLogoNone)
	}
	return b, nil
}

func brandingOut(b brandingState) Branding {
	out := Branding{CompanyLogo: BrandingCompanyLogo(b.CompanyLogo)}
	if b.CompanyLogo == string(BrandingCompanyLogoCustom) {
		out.Version = &b.Version
	}
	return out
}

// parseLogo checks a logo sent as a data URL: a known kind of image, small,
// and for SVG nothing active.
func parseLogo(dataURL string) (contentType string, data []byte, refusal string) {
	_, payload, found := strings.Cut(dataURL, ";base64,")
	if !strings.HasPrefix(dataURL, "data:") || !found {
		return "", nil, "Logo : image en data URL base64 attendue."
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(data) == 0 {
		return "", nil, "Logo illisible."
	}
	if len(data) > maxLogo {
		return "", nil, "Logo trop gros : 512 Ko au plus."
	}
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", data, ""
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		return "image/jpeg", data, ""
	case len(data) > 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp", data, ""
	case bytes.Contains(bytes.ToLower(data), []byte("<svg")):
		if svgActive.Match(data) {
			return "", nil, "Logo SVG refusé : il contient du script ou des liens."
		}
		return "image/svg+xml", data, ""
	}
	return "", nil, "Logo : PNG, JPEG, WebP ou SVG seulement."
}

func (s *Server) GetBranding(ctx context.Context, _ GetBrandingRequestObject) (GetBrandingResponseObject, error) {
	b, err := s.branding(ctx)
	if err != nil {
		return nil, err
	}
	return GetBranding200JSONResponse(brandingOut(b)), nil
}

func (s *Server) UpdateBranding(ctx context.Context, req UpdateBrandingRequestObject) (UpdateBrandingResponseObject, error) {
	if req.Body == nil || !req.Body.CompanyLogo.Valid() {
		return UpdateBranding400ApplicationProblemPlusJSONResponse{badRequest("Choix de logo inconnu.")}, nil
	}
	st := s.db.Load()
	next, err := s.branding(ctx)
	if err != nil {
		return nil, err
	}
	next.CompanyLogo = string(req.Body.CompanyLogo)
	if image := trimmed(req.Body.Image); image != "" {
		contentType, data, refusal := parseLogo(image)
		if refusal != "" {
			return UpdateBranding400ApplicationProblemPlusJSONResponse{badRequest(refusal)}, nil
		}
		if err := st.SetSetting(ctx, logoKey, base64.StdEncoding.EncodeToString(data)); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		next.ContentType, next.Version = contentType, hex.EncodeToString(sum[:8])
	}
	if next.CompanyLogo == string(BrandingCompanyLogoCustom) && next.ContentType == "" {
		return UpdateBranding400ApplicationProblemPlusJSONResponse{badRequest("Choisissez d'abord l'image du logo.")}, nil
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return nil, err
	}
	if err := st.SetSetting(ctx, brandingKey, string(raw)); err != nil {
		return nil, err
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "branding.update", "logo:"+next.CompanyLogo, next.ContentType)
	return UpdateBranding200JSONResponse(brandingOut(next)), nil
}

// GetBrandingLogo serves the logo sent; scripts stay off even when the
// address is opened directly.
func (s *Server) GetBrandingLogo(ctx context.Context, req GetBrandingLogoRequestObject) (GetBrandingLogoResponseObject, error) {
	b, err := s.branding(ctx)
	if err != nil {
		return nil, err
	}
	raw, ok, err := s.db.Load().Setting(ctx, logoKey)
	if err != nil {
		return nil, err
	}
	data, decodeErr := base64.StdEncoding.DecodeString(raw)
	if !ok || b.ContentType == "" || decodeErr != nil {
		return GetBrandingLogo404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Aucun logo envoyé."))}, nil
	}
	cache := "no-cache"
	if req.Params.V != nil && *req.Params.V == b.Version {
		cache = "public, max-age=31536000, immutable"
	}
	csp, nosniff := "default-src 'none'; style-src 'unsafe-inline'; sandbox", "nosniff"
	return GetBrandingLogo200ImageResponse{
		Body: bytes.NewReader(data), ContentType: b.ContentType, ContentLength: int64(len(data)),
		Headers: GetBrandingLogo200ResponseHeaders{CacheControl: &cache, ContentSecurityPolicy: &csp, XContentTypeOptions: &nosniff},
	}, nil
}
