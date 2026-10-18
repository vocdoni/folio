// SPDX-License-Identifier: AGPL-3.0-only

package skill

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The Claude Code CLI is not available on CI runners, so these tests check
// the plugin manifests' shape (`claude plugin validate .` runs when present).

type userOption struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Sensitive   bool   `json:"sensitive,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type mcpServer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

type person struct {
	Name  string `json:"name"`
	URL   string `json:"url,omitempty"`
	Email string `json:"email,omitempty"`
}

type pluginManifest struct {
	Name        string                `json:"name"`
	Version     string                `json:"version"`
	Description string                `json:"description"`
	Author      person                `json:"author"`
	Homepage    string                `json:"homepage"`
	Repository  string                `json:"repository"`
	License     string                `json:"license"`
	Keywords    []string              `json:"keywords"`
	UserConfig  map[string]userOption `json:"userConfig"`
	MCPServers  map[string]mcpServer  `json:"mcpServers"`
}

type marketplaceManifest struct {
	Name     string `json:"name"`
	Owner    person `json:"owner"`
	Metadata struct {
		Description string `json:"description"`
	} `json:"metadata"`
	Plugins []struct {
		Name        string `json:"name"`
		Source      string `json:"source"`
		Description string `json:"description"`
		Category    string `json:"category"`
	} `json:"plugins"`
}

func decodeStrict(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func TestPluginManifest(t *testing.T) {
	var p pluginManifest
	decodeStrict(t, ".claude-plugin/plugin.json", &p)
	if p.Name != "folio" || !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(p.Version) || p.Description == "" ||
		p.License != "AGPL-3.0-only" || p.Author.Name == "" || p.Repository == "" {
		t.Errorf("plugin.json metadata: %+v", p)
	}
	url, tok := p.UserConfig["folio_url"], p.UserConfig["folio_token"]
	if url.Type != "string" || !url.Required || url.Sensitive || url.Title == "" {
		t.Errorf("userConfig.folio_url: %+v", url)
	}
	if tok.Type != "string" || !tok.Required || !tok.Sensitive || tok.Title == "" {
		t.Errorf("userConfig.folio_token must be a required, sensitive string: %+v", tok)
	}
	if len(p.UserConfig) != 2 {
		t.Errorf("userConfig: want exactly folio_url and folio_token, got %d options", len(p.UserConfig))
	}
	s, ok := p.MCPServers["folio"]
	if !ok || len(p.MCPServers) != 1 || s.Type != "http" || s.URL != "${user_config.folio_url}/mcp" ||
		len(s.Headers) != 1 || s.Headers["Authorization"] != "Bearer ${user_config.folio_token}" {
		t.Errorf("mcpServers.folio: %+v", p.MCPServers)
	}
}

func TestMarketplaceManifest(t *testing.T) {
	var m marketplaceManifest
	decodeStrict(t, ".claude-plugin/marketplace.json", &m)
	if m.Name != "folio" || m.Owner.Name == "" || len(m.Plugins) != 1 {
		t.Fatalf("marketplace.json: %+v", m)
	}
	if pl := m.Plugins[0]; pl.Name != "folio" || pl.Source != "./" || pl.Description == "" {
		t.Errorf("marketplace plugin entry: %+v", pl)
	}
}
