package nds

import (
	"reflect"

	"golang.org/x/net/context"
	"google.golang.org/appengine"
	"google.golang.org/appengine/datastore"
	"google.golang.org/appengine/log"
	"google.golang.org/appengine/memcache"
)

// putMultiLimit is the App Engine datastore limit for the maximum number
// of entities that can be put by the datastore.PutMulti at once.
const putMultiLimit = 500

// PutMulti is a batch version of Put. It works just like datastore.PutMulti
// except it interacts appropriately with NDS's caching strategy.
func PutMulti(c context.Context,
	keys []*datastore.Key, vals interface{}) ([]*datastore.Key, error) {

	if err := checkMultiArgs(keys, reflect.ValueOf(vals)); err != nil {
		return nil, err
	}

	return putMulti(c, keys, vals)
}

// Put saves the entity val into the datastore with key. val must be a struct
// pointer; if a struct pointer then any unexported fields of that struct will
// be skipped. If key is an incomplete key, the returned key will be a unique
// key generated by the datastore.
func Put(c context.Context,
	key *datastore.Key, val interface{}) (*datastore.Key, error) {

	keys, err := PutMulti(c, []*datastore.Key{key}, []interface{}{val})
	switch e := err.(type) {
	case nil:
		return keys[0], nil
	case appengine.MultiError:
		return nil, e[0]
	default:
		return nil, err
	}
}

// putMulti puts the entities into the datastore and then its local cache.
func putMulti(c context.Context,
	keys []*datastore.Key, vals interface{}) ([]*datastore.Key, error) {

	lockMemcacheKeys := make([]string, 0, len(keys))
	lockMemcacheItems := make([]*memcache.Item, 0, len(keys))
	for _, key := range keys {
		if !key.Incomplete() {
			item := &memcache.Item{
				Key:        createMemcacheKey(key),
				Flags:      lockItem,
				Value:      itemLock(),
				Expiration: memcacheLockTime,
			}
			lockMemcacheItems = append(lockMemcacheItems, item)
			lockMemcacheKeys = append(lockMemcacheKeys, item.Key)
		}
	}

	if tx, ok := transactionFromContext(c); ok {
		tx.Lock()
		tx.lockMemcacheItems = append(tx.lockMemcacheItems,
			lockMemcacheItems...)
		tx.Unlock()
	} else if err := memcacheSetMulti(c, lockMemcacheItems); err != nil {
		return nil, err
	}

	// Save to the datastore.
	dsKeys, err := datastorePutMulti(c, keys, vals)
	if err != nil {
		return nil, err
	}

	if _, ok := transactionFromContext(c); !ok {
		// Remove the locks.
		if err := memcacheDeleteMulti(c, lockMemcacheKeys); err != nil {
			log.Warningf(c, "putMulti memcache.DeleteMulti %s", err)
		}
	}
	return dsKeys, nil
}
