package db

import (
	"database/sql"
	"sync"
)

var costPolicyLocks sync.Map

// CostPolicyMutex serializes policy mutations, FX conversion and launch snapshots
// for one database. Callers must not recursively acquire this mutex.
func CostPolicyMutex(database *sql.DB) *sync.Mutex {
	value, _ := costPolicyLocks.LoadOrStore(database, &sync.Mutex{})
	return value.(*sync.Mutex)
}
