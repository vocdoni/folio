// SPDX-License-Identifier: AGPL-3.0-only

package httpserver

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/model"
)

// FileSource resolves public file ids (documents.Service implements it).
// Unknown, expired, trashed and purged files return a not_found *model.Error.
type FileSource interface {
	FileForDownload(ctx context.Context, fileID string) (catalog.File, []byte, error)
}

// fileIDRE matches the lowercase UUIDv4 file ids Folio issues; anything else
// is a 404 without a database lookup.
var fileIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// downloads serves GET/HEAD /f/{file_id}/{name}. The file id is the only
// capability; name is cosmetic. Every failure is a bare 404 except storage
// errors (500). Neither the file id nor the name is ever logged.
func downloads(src FileSource, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("file_id")
		if !fileIDRE.MatchString(id) {
			http.NotFound(w, r)
			return
		}
		f, data, err := src.FileForDownload(r.Context(), id)
		var me *model.Error
		switch {
		case errors.As(err, &me) && me.Code == model.ErrNotFound:
			http.NotFound(w, r)
			return
		case err != nil:
			log.ErrorContext(r.Context(), "download failed", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		disposition := "attachment"
		if f.MIME == "application/pdf" || f.MIME == "image/png" {
			disposition = "inline"
		}
		h := w.Header()
		h.Set("Content-Type", f.MIME)
		h.Set("Content-Disposition", contentDisposition(disposition, f.Name))
		h.Set("X-Robots-Tag", "noindex, nofollow")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "private, max-age=300")
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	})
}

// contentDisposition formats the header with the stored file name; names
// that cannot be encoded fall back to "download" plus the extension.
func contentDisposition(kind, name string) string {
	if v := mime.FormatMediaType(kind, map[string]string{"filename": name}); v != "" {
		return v
	}
	ext := ""
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		ext = name[i:]
	}
	return mime.FormatMediaType(kind, map[string]string{"filename": "download" + ext})
}
