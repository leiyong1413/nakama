// Copyright 2017 The Nakama Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"context"

	"strings"

	"database/sql"

	"fmt"

	"encoding/json"

	"encoding/base64"

	"github.com/fatih/structs"
	"github.com/satori/go.uuid"
	"github.com/yuin/gopher-lua"
	"go.uber.org/zap"
)

const CALLBACKS = "runtime_callbacks"

type Callbacks struct {
	HTTP   map[string]*lua.LFunction
	RPC    map[string]*lua.LFunction
	Before map[string]*lua.LFunction
	After  map[string]*lua.LFunction
}

type NakamaModule struct {
	logger *zap.Logger
	db     *sql.DB
}

func NewNakamaModule(logger *zap.Logger, db *sql.DB, l *lua.LState) *NakamaModule {
	l.SetContext(context.WithValue(context.Background(), CALLBACKS, &Callbacks{
		RPC:    make(map[string]*lua.LFunction),
		Before: make(map[string]*lua.LFunction),
		After:  make(map[string]*lua.LFunction),
		HTTP:   make(map[string]*lua.LFunction),
	}))
	return &NakamaModule{
		logger: logger,
		db:     db,
	}
}

func (n *NakamaModule) Loader(l *lua.LState) int {
	mod := l.SetFuncs(l.NewTable(), map[string]lua.LGFunction{
		"logger_info":        n.loggerInfo,
		"logger_warn":        n.loggerWarn,
		"logger_error":       n.loggerError,
		"register_rpc":       n.registerRPC,
		"register_before":    n.registerBefore,
		"register_after":     n.registerAfter,
		"register_http":      n.registerHTTP,
		"user_fetch_id":      n.userFetchId,
		"user_fetch_handle":  n.userFetchHandle,
		"storage_list":       n.storageList,
		"storage_fetch":      n.storageFetch,
		"storage_write":      n.storageWrite,
		"storage_remove":     n.storageRemove,
		"leaderboard_create": n.leaderboardCreate,
	})

	l.Push(mod)
	return 1
}

func (n *NakamaModule) loggerInfo(l *lua.LState) int {
	message := l.CheckString(1)
	if message == "" {
		l.ArgError(1, "expects message string")
		return 0
	}
	n.logger.Info(message)
	l.Push(lua.LString(message))
	return 1
}

func (n *NakamaModule) loggerWarn(l *lua.LState) int {
	message := l.CheckString(1)
	if message == "" {
		l.ArgError(1, "expects message string")
		return 0
	}
	n.logger.Warn(message)
	l.Push(lua.LString(message))
	return 1
}

func (n *NakamaModule) loggerError(l *lua.LState) int {
	message := l.CheckString(1)
	if message == "" {
		l.ArgError(1, "expects message string")
		return 0
	}
	n.logger.Error(message)
	l.Push(lua.LString(message))
	return 1
}

func (n *NakamaModule) registerRPC(l *lua.LState) int {
	fn := l.CheckFunction(1)
	id := l.CheckString(2)

	if id == "" {
		l.ArgError(2, "expects rpc id")
		return 0
	}

	id = strings.ToLower(id)

	rc := l.Context().Value(CALLBACKS).(*Callbacks)
	rc.RPC[id] = fn
	n.logger.Info("Registered RPC function invocation", zap.String("id", id))
	return 0
}

func (n *NakamaModule) registerBefore(l *lua.LState) int {
	fn := l.CheckFunction(1)
	messageName := l.CheckString(2)

	if messageName == "" {
		l.ArgError(2, "expects message name")
		return 0
	}

	messageName = strings.ToLower(messageName)

	foundMessage := false
	for _, v := range RUNTIME_MESSAGES {
		if v == messageName {
			foundMessage = true
			break
		}
	}

	if !foundMessage {
		l.ArgError(2, "Invalid message name for register hook.")
		return 0
	}

	rc := l.Context().Value(CALLBACKS).(*Callbacks)
	rc.Before[messageName] = fn
	n.logger.Info("Registered Before function invocation", zap.String("message", messageName))
	return 0
}

func (n *NakamaModule) registerAfter(l *lua.LState) int {
	fn := l.CheckFunction(1)
	messageName := l.CheckString(2)

	if messageName == "" {
		l.ArgError(2, "expects message name")
		return 0
	}

	messageName = strings.ToLower(messageName)

	foundMessage := false
	for _, v := range RUNTIME_MESSAGES {
		if v == messageName {
			foundMessage = true
			break
		}
	}

	if !foundMessage {
		l.ArgError(2, "Invalid message name for register hook.")
		return 0
	}

	rc := l.Context().Value(CALLBACKS).(*Callbacks)
	rc.After[messageName] = fn
	n.logger.Info("Registered After function invocation", zap.String("message", messageName))
	return 0
}

func (n *NakamaModule) registerHTTP(l *lua.LState) int {
	fn := l.CheckFunction(1)
	path := l.CheckString(2)

	if path == "" {
		l.ArgError(2, "expects http path")
		return 0
	}

	if strings.HasPrefix(path, "/") {
		l.ArgError(2, "http path should not start with leading slash")
		return 0
	}

	path = strings.ToLower(path)

	rc := l.Context().Value(CALLBACKS).(*Callbacks)
	rc.HTTP[path] = fn
	n.logger.Info("Registered HTTP function invocation", zap.String("path", path))
	return 0
}

func (n *NakamaModule) userFetchId(l *lua.LState) int {
	lt := l.CheckTable(1)
	userIds, ok := convertLuaValue(lt).([]interface{})
	if !ok {
		l.ArgError(1, "invalid user id data")
		return 0
	}

	userIdBytes := make([][]byte, 0)
	for _, id := range userIds {
		if ids, ok := id.(string); !ok {
			l.ArgError(1, "each user id must be a string")
			return 0
		} else {
			if uid, err := uuid.FromString(ids); err != nil {
				l.ArgError(1, "invalid user id")
				return 0
			} else {
				userIdBytes = append(userIdBytes, uid.Bytes())
			}
		}
	}

	users, err := UsersFetchIds(n.logger, n.db, userIdBytes)
	if err != nil {
		l.RaiseError(fmt.Sprintf("failed to retrieve users: %s", err.Error()))
		return 0
	}

	//translate uuid to string bytes
	lv := l.NewTable()
	for i, u := range users {
		uid, _ := uuid.FromBytes(u.Id)
		u.Id = []byte(uid.String())
		um := structs.Map(u)
		lv.RawSetInt(i+1, convertValue(l, um))
	}

	l.Push(lv)
	return 1
}

func (n *NakamaModule) userFetchHandle(l *lua.LState) int {
	lt := l.CheckTable(1)
	handles, ok := convertLuaValue(lt).([]interface{})
	if !ok {
		l.ArgError(1, "invalid user handle data")
		return 0
	}

	userHandles := make([]string, 0)
	for _, h := range handles {
		if hs, ok := h.(string); !ok {
			l.ArgError(1, "invalid user handle data, each handle must be a string")
			return 0
		} else {
			userHandles = append(userHandles, hs)
		}
	}

	users, err := UsersFetchHandle(n.logger, n.db, userHandles)
	if err != nil {
		l.RaiseError(fmt.Sprintf("failed to retrieve users: %s", err.Error()))
		return 0
	}

	//translate uuid to string bytes
	lv := l.NewTable()
	for i, u := range users {
		uid, _ := uuid.FromBytes(u.Id)
		u.Id = []byte(uid.String())
		um := structs.Map(u)
		lv.RawSetInt(i+1, convertValue(l, um))
	}

	l.Push(lv)
	return 1
}

func (n *NakamaModule) storageList(l *lua.LState) int {
	var userID []byte
	if us := l.OptString(1, ""); us != "" {
		if uid, err := uuid.FromString(us); err != nil {
			l.ArgError(1, "expects a valid user ID or nil")
			return 0
		} else {
			userID = uid.Bytes()
		}
	}
	bucket := l.OptString(2, "")
	collection := l.OptString(3, "")
	limit := l.CheckInt64(4)
	var cursor []byte
	if cs := l.OptString(5, ""); cs != "" {
		cb, err := base64.StdEncoding.DecodeString(cs)
		if err != nil {
			l.ArgError(5, "cursor is invalid")
			return 0
		}
		cursor = cb
	}

	values, newCursor, _, err := StorageList(n.logger, n.db, uuid.Nil, userID, bucket, collection, limit, cursor)
	if err != nil {
		l.RaiseError(fmt.Sprintf("failed to list storage: %s", err.Error()))
		return 0
	}

	// Convert and push the values.
	lv := l.NewTable()
	for i, v := range values {
		// Convert UUIDs to string representation if needed.
		if len(v.UserId) != 0 {
			uid, _ := uuid.FromBytes(v.UserId)
			v.UserId = []byte(uid.String())
		}
		vm := structs.Map(v)
		lv.RawSetInt(i+1, convertValue(l, vm))
	}
	l.Push(lv)

	// Convert and push the new cursor, if any.
	if len(newCursor) != 0 {
		newCursorString := base64.StdEncoding.EncodeToString(newCursor)
		l.Push(lua.LString(newCursorString))
	} else {
		l.Push(lua.LNil)
	}

	return 2
}

func (n *NakamaModule) storageFetch(l *lua.LState) int {
	keysTable := l.CheckTable(1)
	if keysTable == nil || keysTable.Len() == 0 {
		l.ArgError(1, "expects a valid set of keys")
		return 0
	}
	keysRaw, ok := convertLuaValue(keysTable).([]interface{})
	if !ok {
		l.ArgError(1, "expects a valid set of data")
		return 0
	}
	keyMap := make([]map[string]interface{}, 0)
	for _, d := range keysRaw {
		if m, ok := d.(map[string]interface{}); !ok {
			l.ArgError(1, "expects a valid set of data")
			return 0
		} else {
			keyMap = append(keyMap, m)
		}
	}

	keys := make([]*StorageKey, len(keyMap))
	idx := 0
	for _, k := range keyMap {
		var bucket string
		if b, ok := k["Bucket"]; !ok {
			l.ArgError(1, "expects a bucket in each key")
			return 0
		} else {
			if bs, ok := b.(string); !ok {
				l.ArgError(1, "bucket must be a string")
				return 0
			} else {
				bucket = bs
			}
		}
		var collection string
		if c, ok := k["Collection"]; !ok {
			l.ArgError(1, "expects a collection in each key")
			return 0
		} else {
			if cs, ok := c.(string); !ok {
				l.ArgError(1, "collection must be a string")
				return 0
			} else {
				collection = cs
			}
		}
		var record string
		if r, ok := k["Record"]; !ok {
			l.ArgError(1, "expects a record in each key")
			return 0
		} else {
			if rs, ok := r.(string); !ok {
				l.ArgError(1, "record must be a string")
				return 0
			} else {
				record = rs
			}
		}
		var userID []byte
		if u, ok := k["UserId"]; ok {
			if us, ok := u.(string); !ok {
				l.ArgError(1, "expects valid user IDs in each key, when provided")
				return 0
			} else {
				if uid, err := uuid.FromString(us); err != nil {
					l.ArgError(1, "expects valid user IDs in each key, when provided")
					return 0
				} else {
					userID = uid.Bytes()
				}
			}
		}

		keys[idx] = &StorageKey{
			Bucket:     bucket,
			Collection: collection,
			Record:     record,
			UserId:     userID,
		}
		idx++
	}

	values, _, err := StorageFetch(n.logger, n.db, uuid.Nil, keys)
	if err != nil {
		l.RaiseError(fmt.Sprintf("failed to fetch storage: %s", err.Error()))
		return 0
	}

	lv := l.NewTable()
	for i, v := range values {
		// Convert UUIDs to string representation if needed.
		if len(v.UserId) != 0 {
			uid, _ := uuid.FromBytes(v.UserId)
			v.UserId = []byte(uid.String())
		}
		vm := structs.Map(v)
		lv.RawSetInt(i+1, convertValue(l, vm))
	}

	l.Push(lv)
	return 1
}

func (n *NakamaModule) storageWrite(l *lua.LState) int {
	dataTable := l.CheckTable(1)
	if dataTable == nil || dataTable.Len() == 0 {
		l.ArgError(1, "expects a valid set of data")
		return 0
	}
	dataRaw, ok := convertLuaValue(dataTable).([]interface{})
	if !ok {
		l.ArgError(1, "expects a valid set of data")
		return 0
	}
	dataMap := make([]map[string]interface{}, 0)
	for _, d := range dataRaw {
		if m, ok := d.(map[string]interface{}); !ok {
			l.ArgError(1, "expects a valid set of data")
			return 0
		} else {
			dataMap = append(dataMap, m)
		}
	}

	data := make([]*StorageData, len(dataMap))
	idx := 0
	for _, k := range dataMap {
		var bucket string
		if b, ok := k["Bucket"]; !ok {
			l.ArgError(1, "expects a bucket in each key")
			return 0
		} else {
			if bs, ok := b.(string); !ok {
				l.ArgError(1, "bucket must be a string")
				return 0
			} else {
				bucket = bs
			}
		}
		var collection string
		if c, ok := k["Collection"]; !ok {
			l.ArgError(1, "expects a collection in each key")
			return 0
		} else {
			if cs, ok := c.(string); !ok {
				l.ArgError(1, "collection must be a string")
				return 0
			} else {
				collection = cs
			}
		}
		var record string
		if r, ok := k["Record"]; !ok {
			l.ArgError(1, "expects a record in each key")
			return 0
		} else {
			if rs, ok := r.(string); !ok {
				l.ArgError(1, "record must be a string")
				return 0
			} else {
				record = rs
			}
		}
		var value []byte
		if v, ok := k["Value"]; !ok {
			l.ArgError(1, "expects a value in each key")
			return 0
		} else {
			if vs, ok := v.(string); !ok {
				l.ArgError(1, "value must be a string")
				return 0
			} else {
				value = []byte(vs)
			}
		}
		var userID []byte
		if u, ok := k["UserId"]; ok {
			if us, ok := u.(string); !ok {
				l.ArgError(1, "expects valid user IDs in each value, when provided")
				return 0
			} else {
				uid, err := uuid.FromString(us)
				if err != nil {
					l.ArgError(1, "expects valid user IDs in each value, when provided")
					return 0
				}
				userID = uid.Bytes()
			}
		}
		var version []byte
		if v, ok := k["Version"]; ok {
			if vs, ok := v.(string); !ok {
				l.ArgError(1, "version must be a string")
				return 0
			} else {
				version = []byte(vs)
			}
		}
		readPermission := int64(1)
		if r, ok := k["PermissionRead"]; ok {
			if rf, ok := r.(float64); !ok {
				l.ArgError(1, "permission read must be a number")
				return 0
			} else {
				readPermission = int64(rf)
			}
		}
		writePermission := int64(1)
		if w, ok := k["PermissionWrite"]; ok {
			if wf, ok := w.(float64); !ok {
				l.ArgError(1, "permission read must be a number")
				return 0
			} else {
				writePermission = int64(wf)
			}
		}

		data[idx] = &StorageData{
			Bucket:          bucket,
			Collection:      collection,
			Record:          record,
			UserId:          userID,
			Value:           value,
			Version:         version,
			PermissionRead:  readPermission,
			PermissionWrite: writePermission,
		}
		idx++
	}

	keys, _, err := StorageWrite(n.logger, n.db, uuid.Nil, data)
	if err != nil {
		l.RaiseError(fmt.Sprintf("failed to write storage: %s", err.Error()))
		return 0
	}

	lv := l.NewTable()
	for i, k := range keys {
		km := structs.Map(k)
		lv.RawSetInt(i+1, convertValue(l, km))
	}

	l.Push(lv)
	return 1
}

func (n *NakamaModule) storageRemove(l *lua.LState) int {
	keysTable := l.CheckTable(1)
	if keysTable == nil || keysTable.Len() == 0 {
		l.ArgError(1, "expects a valid set of keys")
		return 0
	}
	keysRaw, ok := convertLuaValue(keysTable).([]interface{})
	if !ok {
		l.ArgError(1, "expects a valid set of data")
		return 0
	}
	keyMap := make([]map[string]interface{}, 0)
	for _, d := range keysRaw {
		if m, ok := d.(map[string]interface{}); !ok {
			l.ArgError(1, "expects a valid set of data")
			return 0
		} else {
			keyMap = append(keyMap, m)
		}
	}

	keys := make([]*StorageKey, len(keyMap))
	idx := 0
	for _, k := range keyMap {
		var bucket string
		if b, ok := k["Bucket"]; !ok {
			l.ArgError(1, "expects a bucket in each key")
			return 0
		} else {
			if bs, ok := b.(string); !ok {
				l.ArgError(1, "bucket must be a string")
				return 0
			} else {
				bucket = bs
			}
		}
		var collection string
		if c, ok := k["Collection"]; !ok {
			l.ArgError(1, "expects a collection in each key")
			return 0
		} else {
			if cs, ok := c.(string); !ok {
				l.ArgError(1, "collection must be a string")
				return 0
			} else {
				collection = cs
			}
		}
		var record string
		if r, ok := k["Record"]; !ok {
			l.ArgError(1, "expects a record in each key")
			return 0
		} else {
			if rs, ok := r.(string); !ok {
				l.ArgError(1, "record must be a string")
				return 0
			} else {
				record = rs
			}
		}
		var userID []byte
		if u, ok := k["UserId"]; ok {
			if us, ok := u.(string); !ok {
				l.ArgError(1, "expects valid user IDs in each key, when provided")
				return 0
			} else {
				uid, err := uuid.FromString(us)
				if err != nil {
					l.ArgError(1, "expects valid user IDs in each key, when provided")
					return 0
				}
				userID = uid.Bytes()
			}
		}
		var version []byte
		if v, ok := k["Version"]; ok {
			if vs, ok := v.(string); !ok {
				l.ArgError(1, "version must be a string")
				return 0
			} else {
				version = []byte(vs)
			}
		}
		keys[idx] = &StorageKey{
			Bucket:     bucket,
			Collection: collection,
			Record:     record,
			UserId:     userID,
			Version:    version,
		}
		idx++
	}

	if _, err := StorageRemove(n.logger, n.db, uuid.Nil, keys); err != nil {
		l.RaiseError(fmt.Sprintf("failed to remove storage: %s", err.Error()))
	}
	return 0
}

func (n *NakamaModule) leaderboardCreate(l *lua.LState) int {
	id := l.CheckString(1)
	sort := l.CheckString(2)
	reset := l.OptString(3, "")
	metadata := l.OptTable(4, l.NewTable())
	authoritative := l.OptBool(5, false)

	leaderboardId, err := uuid.FromString(id)
	if err != nil {
		l.ArgError(1, "invalid leaderboard id")
		return 0
	}

	if sort != "asc" && sort != "desc" {
		l.ArgError(2, "invalid sort - only acceptable values are 'asc' and 'desc'")
		return 0
	}

	metadataMap := ConvertLuaTable(metadata)
	metadataBytes, err := json.Marshal(metadataMap)
	if err != nil {
		l.RaiseError(fmt.Sprintf("failed to convert metadata: %s", err.Error()))
		return 0
	}

	_, err = createLeaderboard(n.logger, n.db, leaderboardId.String(), sort, reset, string(metadataBytes), authoritative)
	if err != nil {
		l.RaiseError(fmt.Sprintf("failed to create leaderboard: %s", err.Error()))
		return 0
	}

	return 0
}
