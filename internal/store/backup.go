// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"fmt"
)

// VacuumInto writes a consistent snapshot of the database to path with
// VACUUM INTO. It runs on the writer connection (the readers are query-only,
// which SQLite treats as read-only for VACUUM INTO), so writes wait for the
// snapshot to finish. path must not exist.
func (db *DB) VacuumInto(ctx context.Context, path string) error {
	if _, err := db.W.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("store: vacuum into: %w", err)
	}
	return nil
}
