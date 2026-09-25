package db

import "database/sql"

// Queryer is satisfied by both *sql.DB and *sql.Tx, so repo functions take
// it and work inside or outside an explicit transaction.
type Queryer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// WithTx runs fn as one unit of work: commit on success, roll back on error
// or panic. Mirrors the Python app's session_scope.
func WithTx(d *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
