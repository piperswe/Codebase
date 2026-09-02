package db

import (
	"testing"

	"github.com/piperswe/Codebase/lib/go/testdb"
)

func TestMigrate(t *testing.T) {
	_ = testdb.New(t, &Migrator)
	// if testdb.New succeeded, then the migrations successfully applied
}
