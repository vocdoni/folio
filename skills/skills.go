// SPDX-License-Identifier: AGPL-3.0-only

// Package skills embeds the generated Folio Agent Skill (skills/folio) so the
// server can offer it for download. The tree is written by
// `go generate ./internal/skill`; only this file is hand-written.
package skills

import "embed"

// FS holds the skill under the top-level directory "folio".
//
//go:embed folio
var FS embed.FS
