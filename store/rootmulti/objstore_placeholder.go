//go:build !objstore
// +build !objstore

package rootmulti

import (
	"fmt"

	"cosmossdk.io/store/types"
	"github.com/crypto-org-chain/cronos/memiavl"
)

func (rs *Store) GetObjKVStore(key types.StoreKey) types.ObjKVStore {
	panic(fmt.Sprintf("object store %s needs the objstore build tag, which this binary was built without; rebuild with make install", key.Name()))
}

func (rs *Store) loadExtraStore(db *memiavl.DB, key types.StoreKey, params storeParams) (types.CommitStore, error) {
	if params.typ == types.StoreTypeObject {
		panic(fmt.Sprintf("object store %s needs the objstore build tag, which this binary was built without; rebuild with make install", key.Name()))
	}
	panic(fmt.Sprintf("unrecognized store type %v", params.typ))
}
