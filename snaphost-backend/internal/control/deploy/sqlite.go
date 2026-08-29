package deploy

import (
	"database/sql"

	"github.com/google/uuid"
)

// uuidPtr renders an optional id for binding. A typed nil pointer is not
// something the driver can convert, so an absent project has to reach it as an
// untyped nil.
func uuidPtr(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return id.String()
}

// rowsAffected reads the count from a result, treating an error as zero.
//
// database/sql returns (int64, error) where the previous driver returned a
// plain int64. Every
// caller here uses the count only to tell "the row was there" from "it was
// not", and a driver that cannot report the count is the second case as far as
// that decision goes — SQLite always can.
func rowsAffected(result sql.Result) int64 {
	n, err := result.RowsAffected()
	if err != nil {
		return 0
	}
	return n
}
