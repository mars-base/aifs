package pgfs

import (
	"context"
	"fmt"

	"github.com/mars-base/aifs/internal/pgfs/meta"
)

// Format initializes a new PG-backed filesystem in the given database.
func Format(ctx context.Context, pgURL, volumeName, tablePrefix string, force bool) (*meta.FormatInfo, error) {
	db, m, _, err := Open(ctx, pgURL, tablePrefix)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	info, err := m.Init(ctx, volumeName, force)
	if err != nil {
		return nil, fmt.Errorf("format failed: %w", err)
	}
	return info, nil
}
