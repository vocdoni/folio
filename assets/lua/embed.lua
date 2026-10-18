-- SPDX-License-Identifier: AGPL-3.0-only
-- Folio's only pandoc Lua filter. Folio rewrites every image target to
-- "assets/<asset-id>.<png|jpg>" after validating it and writes the file into
-- the job directory (pandoc's working directory). Any other target is a bug
-- and aborts the conversion; the filter never reads anything else.
local mime = { png = "image/png", jpg = "image/jpeg" }

function Image(img)
  local id, ext = img.src:match("^assets/([0-9a-f%-]+)%.(%a+)$")
  if not id or not mime[ext] then
    error("folio: unexpected image target " .. img.src)
  end
  local f = assert(io.open(img.src, "rb"))
  local data = f:read("a")
  f:close()
  pandoc.mediabag.insert(img.src, mime[ext], data)
  return nil
end
