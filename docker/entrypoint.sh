#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
#
# Container entrypoint. Folio always runs as the unprivileged `folio` user.
#
# Started as root (Railway with RAILWAY_RUN_UID=0, or `docker run --user 0`),
# it makes FOLIO_DATA_DIR owned by folio and then drops privileges for good
# with setpriv before exec'ing folio (folio uid/gid, inheritable and bounding
# capability sets cleared, no_new_privs). Started as any other user (the
# image default), it execs folio directly.
#
# Ownership is fixed with `find -xdev ! -user folio -exec chown -h`: only
# entries not yet owned by folio are touched, symlinks are changed themselves
# and never followed (a link planted in the volume cannot make root chown a
# file outside it), and other filesystems mounted below the data dir are left
# alone.
set -eu

data="${FOLIO_DATA_DIR:-/data}"

if [ "$(id -u)" = "0" ]; then
	mkdir -p -- "$data"
	find "$data" -xdev ! -user folio -exec chown -h folio:folio {} +
	exec setpriv --reuid=folio --regid=folio --init-groups --inh-caps=-all --bounding-set=-all \
		--no-new-privs -- /usr/local/bin/folio "$@"
fi

if [ ! -w "$data" ]; then
	echo "folio: $data is not writable by uid $(id -u). Run the container as root" \
		"(RAILWAY_RUN_UID=0 on Railway, --user 0 with docker) so the entrypoint can" \
		"give the volume to the folio user, or chown it to uid 47100 yourself." >&2
	exit 1
fi
exec /usr/local/bin/folio "$@"
