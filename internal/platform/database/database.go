// Package database provides functions to manage the LMDB wrapper for the application.
package database

import (
	"github.com/Data-Corruption/lmdb-go/lmdb"
	"github.com/Data-Corruption/lmdb-go/wrap"
	"github.com/Data-Corruption/stdx/xlog"
)

/*
Notes on adding new DBIs:
  - Existing data is preserved, no migration is needed.
  - Removing a DBI from the list won't delete it from the LMDB file.
    The data will still exist on disk, you just won't have a handle to access it.
    You'd need to explicitly drop the database if you wanted to reclaim space.
  - MaxNamedDBs is set to 128 in Data-Corruption/lmdb-go/wrap.
    If you need more, you'll need to use the raw lmdb-go package.
    But at that point, you should probably be using a different database.
*/
var (
	ConfigDBI = register("config")
	// MyNewDBI = register("mynew") // example
)

/* KV Layout:

Config
    "version" -> version string of database schema (not app version)
	"data" -> marshaled config struct
Other DBIs
    "<name>" -> <data>

*/

const (
	ConfigVersionKey = "version"
	ConfigDataKey    = "data"
)

// dbiEntry holds a DBI name and a pointer to its cached handle.
type dbiEntry struct {
	name   string
	handle *lmdb.DBI
}

// dbiRegistry holds all registered DBIs. Populated at init time via register().
var dbiRegistry []dbiEntry

// register adds a DBI to the registry and returns a pointer to its handle.
func register(name string) *lmdb.DBI {
	handle := new(lmdb.DBI)
	dbiRegistry = append(dbiRegistry, dbiEntry{name: name, handle: handle})
	return handle
}

// DBINameList returns a slice of all registered DBI names for initialization.
func DBINameList() []string {
	names := make([]string, len(dbiRegistry))
	for i, entry := range dbiRegistry {
		names[i] = entry.name
	}
	return names
}

func New(directory string, logger *xlog.Logger) (*wrap.DB, error) {
	// Initialize LMDB with the specified DBIs
	db, srClosed, err := wrap.New(directory, DBINameList())
	if err != nil {
		if db != nil {
			db.Close()
		}
		return nil, err
	}
	logger.Infof("LMDB initialized at %s", directory)
	if srClosed > 0 {
		logger.Warnf("LMDB had %d stale readers which were closed", srClosed)
	}

	// Cache DBIs
	if err := cacheDBIs(db); err != nil {
		return nil, err
	}

	// Perform migrations if needed
	if err := Migrate(db, logger); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func cacheDBIs(db *wrap.DB) error {
	dbis := db.GetDBis()
	for _, entry := range dbiRegistry {
		*entry.handle = dbis[entry.name]
	}
	return nil
}
