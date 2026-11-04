package store

import (
	"context"
	"database/sql"
	"github.com/jmoiron/sqlx"
)

// BoolInt matches the existing INTEGER flags in both supported database schemas.
func BoolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// NamedExec encodes boolean application fields as integer database flags. pgx
// does not implicitly encode Go bool into PostgreSQL integer columns.
func NamedExec(ctx context.Context, db *sqlx.DB, query string, arg any) (sql.Result, error) {
	q, args, e := sqlx.Named(query, arg)
	if e != nil {
		return nil, e
	}
	for i, v := range args {
		switch b := v.(type) {
		case bool:
			args[i] = BoolInt(b)
		case *bool:
			if b != nil {
				args[i] = BoolInt(*b)
			} else {
				args[i] = nil
			}
		}
	}
	return db.ExecContext(ctx, db.Rebind(q), args...)
}
