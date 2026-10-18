// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"bytes"
	"fmt"
	"image"
	"image/png"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/image/draw"
)

const (
	// MaxImages is the most page images returned as image content per call.
	MaxImages = 6
	// MaxImageWidth is the widest page image returned as content (px).
	MaxImageWidth = 800
)

// imageContent downscales a PNG page to at most MaxImageWidth pixels wide
// and returns it as MCP image content.
func imageContent(pngData []byte) (*mcp.ImageContent, error) {
	src, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, fmt.Errorf("mcpserver: decode page png: %w", err)
	}
	b := src.Bounds()
	if b.Dx() <= MaxImageWidth {
		return &mcp.ImageContent{MIMEType: "image/png", Data: pngData}, nil
	}
	h := max(1, b.Dy()*MaxImageWidth/b.Dx())
	dst := image.NewRGBA(image.Rect(0, 0, MaxImageWidth, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, fmt.Errorf("mcpserver: encode page png: %w", err)
	}
	return &mcp.ImageContent{MIMEType: "image/png", Data: buf.Bytes()}, nil
}
