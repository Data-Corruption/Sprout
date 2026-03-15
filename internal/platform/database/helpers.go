package database

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Data-Corruption/lmdb-go/lmdb"
	"github.com/Data-Corruption/lmdb-go/wrap"
	"github.com/fxamacker/cbor/v2"
)

// helpers.go is a collection of helper functions for the database package.
// It doubles as reference for when i need to do custom database operations.

var ErrNotFound = errors.New("key not found in the db")

// FilterFunc receives raw key/value bytes BEFORE unmarshalling.
// Return true to process the entry, false to skip it. Pass nil to process all entries.
// This is useful for key prefix filtering without the cost of unmarshalling skipped entries.
type FilterFunc func(key, value []byte) bool

// ForUpdateAction specifies what to do with an entry after the callback.
// If an update or delete action are passed along with break being true,
// the function will return before performing the action.
type ForUpdateAction int

const (
	ForUpdateNothing ForUpdateAction = iota // do nothing
	ForUpdateUpdate                         // re-marshal and store entry
	ForUpdateDelete                         // remove entry
)

// EncodingType is the type of encoding to use for a value.
type EncodingType int

const (
	EncodingJSON EncodingType = iota
	EncodingCBOR
)

func (e EncodingType) String() string {
	switch e {
	case EncodingJSON:
		return "json"
	case EncodingCBOR:
		return "cbor"
	}
	return "unknown"
}

// ---- Transaction helpers ---------------------------------------------------

// encode marshals the value. The value can be passed by type or pointer, all encoding types support it.
func encode(value any, encoding EncodingType) ([]byte, error) {
	switch encoding {
	case EncodingJSON:
		return json.Marshal(value)
	case EncodingCBOR:
		return cbor.Marshal(value)
	}
	return nil, fmt.Errorf("unknown encoding type: %v", encoding)
}

func decode(data []byte, valuePtr any, encoding EncodingType) error {
	switch encoding {
	case EncodingJSON:
		return json.Unmarshal(data, valuePtr)
	case EncodingCBOR:
		return cbor.Unmarshal(data, valuePtr)
	}
	return fmt.Errorf("unknown encoding type: %v", encoding)
}

// TxnPut marshals and stores a value by pointer in the database with the given encoding.
func TxnPut(txn *lmdb.Txn, dbi lmdb.DBI, key []byte, valuePtr any, encoding EncodingType) error {
	data, err := encode(valuePtr, encoding)
	if err != nil {
		return fmt.Errorf("failure writing to db: %w", err)
	}
	return txn.Put(dbi, key, data, 0)
}

// TxnGet retrieves a value from the database and unmarshals it into the provided value pointer
// using the given encoding. Returns ErrNotFound if the key was not found in the database.
func TxnGet(txn *lmdb.Txn, dbi lmdb.DBI, key []byte, valuePtr any, encoding EncodingType) error {
	buf, err := txn.Get(dbi, key)
	if err != nil {
		if lmdb.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("failure reading from db: %w", err)
	}
	return decode(buf, valuePtr, encoding)
}

// TxnDel removes a key from the database within an existing transaction.
// Returns nil if key doesn't exist (idempotent).
func TxnDel(txn *lmdb.Txn, dbi lmdb.DBI, key []byte) error {
	err := txn.Del(dbi, key, nil)
	if err == nil {
		return nil
	}
	if lmdb.IsNotFound(err) {
		return nil // Idempotent
	}
	return fmt.Errorf("failure deleting key: %w", err)
}

// TxnExists checks if a key exists in the database within an existing transaction.
func TxnExists(txn *lmdb.Txn, dbi lmdb.DBI, key []byte) (bool, error) {
	_, err := txn.Get(dbi, key)
	if err == nil {
		return true, nil
	}
	if lmdb.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("failure to check if key exists: %w", err)
}

// TxnCount returns the number of entries in a DBI within an existing transaction.
func TxnCount(txn *lmdb.Txn, dbi lmdb.DBI) (uint64, error) {
	stat, err := txn.Stat(dbi)
	if err != nil {
		return 0, fmt.Errorf("failed to acquire stat on dbi: %w", err)
	}
	return stat.Entries, nil
}

// TxnUpsert updates a value in the database using the provided update function,
// creating it with defaultFn if it does not exist.
// Returns a pointer to the updated value and a boolean indicating if the value was created.
func TxnUpsert[T any](txn *lmdb.Txn, dbi lmdb.DBI, key []byte, defaultFn func() T, updateFn func(*T) error, encoding EncodingType) (*T, bool, error) {
	created := false

	var value T
	err := TxnGet(txn, dbi, key, &value, encoding)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return nil, false, fmt.Errorf("failed to get value in upsert: %w", err)
		}
		created = true
		value = defaultFn()
	}

	if err := updateFn(&value); err != nil {
		return nil, false, fmt.Errorf("upsert callback function failed: %w", err)
	}

	if err := TxnPut(txn, dbi, key, &value, encoding); err != nil {
		return nil, false, fmt.Errorf("failed to upsert value: %w", err)
	}

	return &value, created, nil
}

// TxnUpdate updates a value in the database using the provided update function.
// Returns a pointer to the updated value.
func TxnUpdate[T any](txn *lmdb.Txn, dbi lmdb.DBI, key []byte, updateFn func(*T) error, encoding EncodingType) (*T, error) {
	var value T
	if err := TxnGet(txn, dbi, key, &value, encoding); err != nil {
		return nil, fmt.Errorf("failed to get value in update: %w", err)
	}

	if err := updateFn(&value); err != nil {
		return nil, fmt.Errorf("update function failed: %w", err)
	}

	if err := TxnPut(txn, dbi, key, &value, encoding); err != nil {
		return nil, fmt.Errorf("failed to update value: %w", err)
	}

	return &value, nil
}

// TxnForEachView iterates over all entries in a DBI within an existing transaction, applying the callback to each.
// The callback receives the key and a pointer to the unmarshaled value. See [FilterFunc] for more information.
// Returning true or an error will stop the iterating and return.
func TxnForEachView[T any](
	txn *lmdb.Txn,
	dbi lmdb.DBI,
	reverse bool,
	filter FilterFunc,
	callback func(key []byte, value *T) (bool, error),
	encoding EncodingType,
) error {
	cursor, err := txn.OpenCursor(dbi)
	if err != nil {
		return fmt.Errorf("failed to create cursor: %w", err)
	}
	defer cursor.Close()

	var start uint = lmdb.First
	var dir uint = lmdb.Next
	if reverse {
		start = lmdb.Last
		dir = lmdb.Prev
	}

	k, v, err := cursor.Get(nil, nil, start)
	for ; !lmdb.IsNotFound(err); k, v, err = cursor.Get(nil, nil, dir) {
		if err != nil {
			return fmt.Errorf("failed to get entry: %w", err)
		}

		// Apply filter if provided
		if filter != nil && !filter(k, v) {
			continue
		}

		// decode entry
		var value T
		if err := decode(v, &value, encoding); err != nil {
			return fmt.Errorf("failed to unmarshal entry: %w", err)
		}

		// Run callback
		shouldBreak, err := callback(k, &value)
		if err != nil {
			return fmt.Errorf("callback failed: %w", err)
		}
		if shouldBreak {
			return nil
		}
	}
	return nil
}

// TxnForEachUpdate iterates over all entries in a DBI within an existing transaction, applying the callback to each.
// The callback receives the key and a pointer to the unmarshaled value. See [FilterFunc] for more information.
// Return a ForUpdateAction to indicate what to do with the entry, bool indicating if iteration should stop and return, or an err.
// If the bool is true or err is not nil the action is ignored and the function returns.
func TxnForEachUpdate[T any](
	txn *lmdb.Txn,
	dbi lmdb.DBI,
	reverse bool,
	filter FilterFunc,
	callback func(key []byte, value *T) (ForUpdateAction, bool, error),
	encoding EncodingType,
) error {
	cursor, err := txn.OpenCursor(dbi)
	if err != nil {
		return fmt.Errorf("failed to create cursor: %w", err)
	}
	defer cursor.Close()

	var start uint = lmdb.First
	var dir uint = lmdb.Next
	if reverse {
		start = lmdb.Last
		dir = lmdb.Prev
	}

	k, v, err := cursor.Get(nil, nil, start)
	for ; !lmdb.IsNotFound(err); k, v, err = cursor.Get(nil, nil, dir) {
		if err != nil {
			return fmt.Errorf("failed to get entry: %w", err)
		}

		// Apply filter if provided
		if filter != nil && !filter(k, v) {
			continue
		}

		// decode entry
		var value T
		if err := decode(v, &value, encoding); err != nil {
			return fmt.Errorf("failed to unmarshal entry: %w", err)
		}

		// Run callback
		action, shouldBreak, err := callback(k, &value)
		if err != nil {
			return fmt.Errorf("callback failed: %w", err)
		}
		if shouldBreak {
			return nil
		}

		switch action {
		case ForUpdateUpdate:
			data, err := encode(&value, encoding)
			if err != nil {
				return fmt.Errorf("failed to marshal entry: %w", err)
			}
			if err := cursor.Put(k, data, lmdb.Current); err != nil {
				return fmt.Errorf("failed to update entry: %w", err)
			}
		case ForUpdateDelete:
			// Deleting might invalidate the cursor, keep in mind.
			if err := cursor.Del(0); err != nil {
				return fmt.Errorf("failed to delete entry: %w", err)
			}
		}
	}
	return nil
}

// TxnList retrieves all entries from a DBI as a slice within an existing transaction.
//
// The optional filter function receives raw key/value bytes BEFORE unmarshalling.
// Return true to include the entry, false to skip it. Pass nil to include all entries.
// This is useful for key prefix filtering without the cost of unmarshalling skipped entries.
func TxnList[T any](txn *lmdb.Txn, dbi lmdb.DBI, filter FilterFunc, encoding EncodingType) ([]T, error) {
	var result []T
	err := TxnForEachView(txn, dbi, false, filter, func(key []byte, value *T) (bool, error) {
		result = append(result, *value)
		return false, nil
	}, encoding)
	return result, err
}

// ---- Convenience wrappers (don't nest in a txn!) ---------------------------

// Get retrieves a copy of a value from the database.
// lmdb.IsNotFound(err) will be true if the key was not found.
//
// WARNING: Starts a transaction. Use TxnGet if you need to compose multiple operations.
func Get[T any](db *wrap.DB, dbi lmdb.DBI, key []byte, encoding EncodingType) (*T, error) {
	var value T
	if err := db.View(func(txn *lmdb.Txn) error {
		return TxnGet(txn, dbi, key, &value, encoding)
	}); err != nil {
		return nil, err
	}
	return &value, nil
}

// Put marshals and stores a value in the database.
//
// WARNING: Starts a transaction. Use TxnPut if you need to compose multiple operations.
// If an error is returned, the transaction is rolled back and nothing is persisted.
func Put[T any](db *wrap.DB, dbi lmdb.DBI, key []byte, value *T, encoding EncodingType) error {
	return db.Update(func(txn *lmdb.Txn) error {
		return TxnPut(txn, dbi, key, value, encoding)
	})
}

// Del removes a key from the database.
// Returns nil if key doesn't exist (idempotent).
//
// WARNING: Starts a transaction. Use TxnDel if you need to compose multiple operations.
// If an error is returned, the transaction is rolled back and nothing is persisted.
func Del(db *wrap.DB, dbi lmdb.DBI, key []byte) error {
	return db.Update(func(txn *lmdb.Txn) error {
		return TxnDel(txn, dbi, key)
	})
}

// Exists checks if a key exists in the database.
//
// WARNING: Starts a transaction. Use TxnExists if you need to compose multiple operations.
func Exists(db *wrap.DB, dbi lmdb.DBI, key []byte) (bool, error) {
	var exists bool
	err := db.View(func(txn *lmdb.Txn) error {
		var err error
		exists, err = TxnExists(txn, dbi, key)
		return err
	})
	return exists, err
}

// Count returns the number of entries in a DBI.
//
// WARNING: Starts a transaction. Use TxnCount if you need to compose multiple operations.
func Count(db *wrap.DB, dbi lmdb.DBI) (uint64, error) {
	var count uint64
	err := db.View(func(txn *lmdb.Txn) error {
		var err error
		count, err = TxnCount(txn, dbi)
		return err
	})
	return count, err
}

// List retrieves all entries from a DBI as a slice.
// Useful for small DBIs like roles where you need all entries.
//
// The optional filter function receives raw key/value bytes BEFORE unmarshalling.
// Return true to include the entry, false to skip it. Pass nil to include all entries.
// This is useful for key prefix filtering without the cost of unmarshalling skipped entries.
//
// WARNING: Starts a transaction. Use TxnList if you need to compose multiple operations.
func List[T any](db *wrap.DB, dbi lmdb.DBI, filter FilterFunc, encoding EncodingType) ([]T, error) {
	var result []T
	err := db.View(func(txn *lmdb.Txn) error {
		var err error
		result, err = TxnList[T](txn, dbi, filter, encoding)
		return err
	})
	return result, err
}

// Upsert updates a value in the database using the provided update function,
// creating it with defaultFn if it does not exist.
// Returns true if the value was created.
//
// WARNING: Starts a transaction. Use TxnUpsert if you need to compose multiple operations.
// If updateFn returns an error, the transaction is rolled back and nothing is persisted.
func Upsert[T any](db *wrap.DB, dbi lmdb.DBI, key []byte, defaultFn func() T, updateFn func(*T) error, encoding EncodingType) (*T, bool, error) {
	var value *T
	var created bool
	err := db.Update(func(txn *lmdb.Txn) error {
		var err error
		value, created, err = TxnUpsert(txn, dbi, key, defaultFn, updateFn, encoding)
		return err
	})
	return value, created, err
}

// Update updates a value in the database using the provided update function and returns the updated value.
//
// WARNING: Starts a transaction. Use TxnUpdate if you need to compose multiple operations.
// If updateFn returns an error, the transaction is rolled back and nothing is persisted.
func Update[T any](db *wrap.DB, dbi lmdb.DBI, key []byte, updateFn func(*T) error, encoding EncodingType) (*T, error) {
	var value *T
	err := db.Update(func(txn *lmdb.Txn) error {
		var err error
		value, err = TxnUpdate(txn, dbi, key, updateFn, encoding)
		return err
	})
	return value, err
}

// ForEachView iterates over all entries in a DBI, running the callback on each.
// The callback receives the key and a pointer to the unmarshaled value. See [FilterFunc] for more information.
// Returning true or an error will stop the iterating and return.
//
// WARNING: Starts a transaction. Use TxnForEachView if you need to compose multiple operations.
// If the callback returns an error, the transaction is rolled back and nothing is persisted.
func ForEachView[T any](
	db *wrap.DB,
	dbi lmdb.DBI,
	reverse bool,
	filter FilterFunc,
	callback func(key []byte, value *T) (bool, error),
	encoding EncodingType,
) error {
	return db.View(func(txn *lmdb.Txn) error {
		return TxnForEachView(txn, dbi, reverse, filter, callback, encoding)
	})
}

// ForEachUpdate iterates over all entries in a DBI, running the callback on each.
// The callback receives the key and a pointer to the unmarshaled value. See [FilterFunc] for more information.
// Return a ForUpdateAction to indicate what to do with the entry, bool indicating if iteration should stop and return, or an err.
// If the bool is true or err is not nil the action is ignored and the function returns.
//
// WARNING: Starts a transaction. Use TxnForEachUpdate if you need to compose multiple operations.
// If the callback returns an error, the transaction is rolled back and nothing is persisted.
func ForEachUpdate[T any](
	db *wrap.DB,
	dbi lmdb.DBI,
	reverse bool,
	filter FilterFunc,
	callback func(key []byte, value *T) (ForUpdateAction, bool, error),
	encoding EncodingType,
) error {
	return db.Update(func(txn *lmdb.Txn) error {
		return TxnForEachUpdate(txn, dbi, reverse, filter, callback, encoding)
	})
}
