// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"bytes"
	"context"
	"image"
	_ "image/jpeg" // logo decoding
	"image/png"
	"net/http"
	"strings"

	bundled "github.com/vocdoni/folio/assets"
	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/ai"
	"github.com/vocdoni/folio/internal/assets"
	"github.com/vocdoni/folio/internal/model"
)

// Web page evidence fetch limits.
const (
	pageMaxBytes  = 2 << 20
	sheetMaxBytes = 512 << 10
	logoMaxPNG    = 2 << 20 // logo images larger than this are not sent to the model
)

var htmlAccept = http.Header{
	"Accept":     {"text/html,application/xhtml+xml;q=0.9,*/*;q=0.5"},
	"User-Agent": {"Mozilla/5.0 (compatible; Folio brand assistant)"},
}

// BrandSuggestIn is POST /ui/api/ai/brand-suggest: exactly one of an
// uploaded logo asset or an https web page.
type BrandSuggestIn struct {
	AssetID string `json:"asset_id,omitempty"`
	URL     string `json:"url,omitempty"`
}

// BrandSuggestOut is the job result: a brand proposal (not saved), its
// validation findings and the evidence it came from.
type BrandSuggestOut struct {
	Brand     model.Brand     `json:"brand"`
	Findings  []model.Finding `json:"findings"`
	Swatches  []ai.Swatch     `json:"swatches,omitempty"` // logo colours
	Evidence  *ai.Evidence    `json:"evidence,omitempty"` // web page evidence
	LogoIndex int             `json:"logo_index"`         // suggested logo among evidence.images, -1 for none
}

// slug turns a name into a brand id.
func slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
		if b.Len() >= 40 {
			break
		}
	}
	id := strings.Trim(b.String(), "-")
	if len(id) < 2 {
		id = "brand"
	}
	return id
}

func (s *Server) handleBrandSuggest(w http.ResponseWriter, r *http.Request) {
	var in BrandSuggestIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if (in.AssetID == "") == (in.URL == "") {
		writeError(w, r, invalidf("send exactly one of asset_id (an uploaded logo) or url (an https web page)"))
		return
	}
	if in.URL != "" && !strings.HasPrefix(strings.ToLower(in.URL), "https://") {
		writeError(w, r, invalidf("the web page must be an https:// URL"))
		return
	}
	s.startJob(w, r, "brand-suggest", func(ctx context.Context, step func(string)) (any, error) {
		bi := ai.BrandInput{Fonts: bundled.FontFamilies}
		out := BrandSuggestOut{LogoIndex: -1}
		if in.AssetID != "" {
			step("reading the logo colours")
			a, err := s.o.Assets.Get(ctx, in.AssetID)
			if err != nil || a.Deleted {
				return nil, &model.Error{Code: model.ErrNotFound, Message: "the logo asset does not exist"}
			}
			ra, err := s.o.Assets.Asset(ctx, in.AssetID, false)
			if err != nil {
				return nil, err
			}
			raster := ra.Data
			if ra.PNG != nil {
				raster = ra.PNG
			}
			img, _, err := image.Decode(bytes.NewReader(raster))
			if err != nil {
				return nil, invalidf("the logo could not be decoded")
			}
			bi.NameHint, bi.Swatches = strings.TrimSuffix(a.Name, extOf(a.MIME)), ai.DominantColors(img, 6)
			out.Swatches = bi.Swatches
			if s.vision.Load() && len(raster) <= logoMaxPNG {
				if ra.MIME == "image/jpeg" {
					var buf bytes.Buffer
					if png.Encode(&buf, img) == nil {
						raster = buf.Bytes()
					}
				}
				bi.LogoPNG = raster
			}
		} else {
			step("reading the web page")
			ev, err := s.pageEvidence(ctx, in.URL)
			if err != nil {
				return nil, err
			}
			bi.Evidence, out.Evidence = ev, ev
		}
		p, err := s.o.AI.SuggestBrand(ctx, bi, step)
		if err != nil {
			return nil, err
		}
		out.LogoIndex = p.LogoIndex
		out.Brand = model.Brand{ID: slug(p.Name), Name: p.Name, Colors: p.Colors, Fonts: p.Fonts,
			Header: model.BrandHeader{Text: p.Header}, Footer: model.BrandFooter{Text: p.Footer, PageNumbers: true}}
		if in.AssetID != "" {
			out.Brand.Logo = &model.BrandLogo{AssetID: in.AssetID, WidthMM: 35}
			out.Brand.Header.ShowLogo = true
		}
		out.Findings = s.o.Catalog.ValidateBrand(ctx, out.Brand)
		if out.Findings == nil {
			out.Findings = []model.Finding{}
		}
		return out, nil
	})
}

// pageEvidence fetches an https page and up to three same-origin
// stylesheets through the SSRF-guarded fetcher and extracts the evidence.
func (s *Server) pageEvidence(ctx context.Context, rawURL string) (*ai.Evidence, error) {
	page, final, _, err := assets.NewFetcher(pageMaxBytes, s.o.FetchOptions...).Get(ctx, rawURL, htmlAccept)
	if err != nil {
		return nil, invalidf("the page could not be read: %v", err)
	}
	ev, sheets := ai.ExtractEvidence(page, final)
	sf := assets.NewFetcher(sheetMaxBytes, s.o.FetchOptions...)
	for _, u := range sheets {
		if css, _, _, err := sf.Get(ctx, u, http.Header{"Accept": {"text/css,*/*;q=0.1"}}); err == nil {
			ev.AddCSS(string(css))
		}
	}
	return ev, nil
}

// BrandLogoIn is POST /ui/api/ai/brand-logo: upload the logo candidate the
// user picked from a finished brand-suggest job.
type BrandLogoIn struct {
	JobID string `json:"job_id"`
	Index int    `json:"index"`
}

func (s *Server) handleBrandLogo(w http.ResponseWriter, r *http.Request) {
	var in BrandLogoIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	res, ok := s.jobs.result(in.JobID, sessionOf(r).key)
	out, isBrand := res.(BrandSuggestOut)
	if !ok || !isBrand || out.Evidence == nil {
		writeError(w, r, &model.Error{Code: model.ErrNotFound, Message: "no finished web page suggestion with that job id in this session"})
		return
	}
	if in.Index < 0 || in.Index >= len(out.Evidence.Images) {
		writeError(w, r, invalidf("index must be between 0 and %d", len(out.Evidence.Images)-1))
		return
	}
	// Only a URL Folio extracted itself is fetched, never one the model wrote.
	a, err := s.o.Assets.Upload(r.Context(), Actor, assets.UploadRequest{Name: "logo" + pathExt(out.Evidence.Images[in.Index]), URL: out.Evidence.Images[in.Index]})
	if err != nil {
		writeError(w, r, err)
		return
	}
	noteActivity(r, a.ID, activity.Detail{Bytes: int64(a.Size)})
	writeJSON(w, http.StatusOK, assetOut(*a))
}

func pathExt(u string) string {
	u = strings.SplitN(strings.SplitN(u, "?", 2)[0], "#", 2)[0]
	if i := strings.LastIndexByte(u, '.'); i > strings.LastIndexByte(u, '/') && len(u)-i <= 5 {
		return strings.ToLower(u[i:])
	}
	return ""
}
