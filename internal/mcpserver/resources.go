// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vocdoni/folio/internal/model"
)

const (
	guideURIPrefix    = "folio://guide/"
	templateURIPrefix = "folio://templates/"
	documentURIPrefix = "folio://documents/"
)

// resourceError maps not_found to the MCP "resource not found" error; other
// failures are logged and returned without internal detail.
func (d *Deps) resourceError(ctx context.Context, uri string, err error) error {
	var me *model.Error
	if errors.As(err, &me) && me.Code == model.ErrNotFound {
		return mcp.ResourceNotFoundError(uri)
	}
	d.Logger.ErrorContext(ctx, "resource read failed", "uri_kind", strings.SplitN(strings.TrimPrefix(uri, "folio://"), "/", 2)[0], "err", err)
	return errors.New("internal error reading the resource")
}

func jsonResource(uri string, v any) (*mcp.ReadResourceResult, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(raw)}}}, nil
}

func registerResources(s *mcp.Server, d *Deps) {
	readGuide := func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := req.Params.URI
		md, ok := Guide(strings.TrimPrefix(uri, guideURIPrefix))
		if !ok {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "text/markdown", Text: md}}}, nil
	}
	for _, t := range Topics {
		md, _ := Guide(t)
		title, summary := guideHead(md)
		s.AddResource(&mcp.Resource{URI: guideURIPrefix + t, Name: "guide-" + t, Title: title, Description: summary, MIMEType: "text/markdown"}, readGuide)
	}
	s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: guideURIPrefix + "{topic}", Name: "guide", Title: "Folio guide",
		Description: "Authoring guide by topic (same text as get_guide).", MIMEType: "text/markdown"}, readGuide)

	s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: templateURIPrefix + "{id}", Name: "template", Title: "Folio template",
		Description: "Latest version of a template as JSON (same as get_template).", MIMEType: "application/json"},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			uri := req.Params.URI
			id := strings.TrimPrefix(uri, templateURIPrefix)
			if !model.SlugRE.MatchString(id) {
				return nil, mcp.ResourceNotFoundError(uri)
			}
			it, err := d.Catalog.GetTemplate(ctx, id, 0)
			if err != nil {
				return nil, d.resourceError(ctx, uri, err)
			}
			return jsonResource(uri, GetTemplateOut{Template: it.Spec, Version: it.Version, LatestVersion: it.LatestVersion, Archived: it.Archived, Versions: it.Versions})
		})

	s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: documentURIPrefix + "{id}", Name: "document", Title: "Folio document",
		Description: "A document with its latest revision as JSON (same as get_document without images).", MIMEType: "application/json"},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			uri := req.Params.URI
			id := strings.TrimPrefix(uri, documentURIPrefix)
			doc, err := d.Documents.Get(ctx, id, 0)
			if err != nil {
				return nil, d.resourceError(ctx, uri, err)
			}
			out, err := (&call{d: d}).documentOut(ctx, doc, GetDocumentIn{ID: id, IncludePreviews: previewsNone})
			if err != nil {
				return nil, d.resourceError(ctx, uri, err)
			}
			return jsonResource(uri, out)
		})
}
