package codemode

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"time"

	lua "github.com/Shopify/go-lua"
)

// toGo converts the Lua value at index into a plain Go value:
// nil, bool, float64, string, []any (contiguous array) or map[string]any.
func toGo(l *lua.State, index int) any {
	switch l.TypeOf(index) {
	case lua.TypeNone, lua.TypeNil:
		return nil
	case lua.TypeBoolean:
		return l.ToBoolean(index)
	case lua.TypeNumber:
		n, _ := l.ToNumber(index)
		return n
	case lua.TypeString:
		s, _ := l.ToString(index)
		return s
	case lua.TypeTable:
		return tableToGo(l, index)
	default:
		return nil
	}
}

func tableToGo(l *lua.State, index int) any {
	index = l.AbsIndex(index) // the traversal pushes keys/values, shifting top-relative indices
	arr := make([]any, 0, 8)
	m := map[string]any{}
	isArray := true
	length := 0
	l.Length(index)
	if n, ok := l.ToNumber(-1); ok {
		length = int(n)
	}
	l.Pop(1)

	l.PushNil()
	for l.Next(index) {
		// Numeric consecutive keys go to the array; the rest to the map.
		if k, ok := l.ToNumber(-2); ok {
			if i := int(k); float64(i) == k && i == len(arr)+1 {
				arr = append(arr, toGo(l, -1))
				l.Pop(1)
				continue
			}
			isArray = false
			m[strconv.FormatFloat(k, 'g', -1, 64)] = toGo(l, -1)
			l.Pop(1)
			continue
		}
		if s, ok := l.ToString(-2); ok {
			m[s] = toGo(l, -1)
		}
		l.Pop(1)
	}
	if isArray && len(m) == 0 && (len(arr) > 0 || length == 0) {
		if len(arr) == 0 {
			return []any{}
		}
		return arr
	}
	return m
}

// pushGo pushes a Go value as a Lua value. Supported: nil, bool, integers,
// floats, strings, []any, map[string]any, time.Time (RFC3339), error (message),
// and anything that json round-trips into those (json.RawMessage, structs...).
func pushGo(l *lua.State, v any) {
	switch t := v.(type) {
	case nil:
		l.PushNil()
	case bool:
		l.PushBoolean(t)
	case int:
		l.PushInteger(t)
	case int64:
		l.PushInteger(int(t))
	case float64:
		l.PushNumber(t)
	case float32:
		l.PushNumber(float64(t))
	case string:
		l.PushString(t)
	case []any:
		l.CreateTable(len(t), 0)
		for i, item := range t {
			l.PushInteger(i + 1)
			pushGo(l, item)
			l.SetTable(-3)
		}
	case map[string]any:
		l.CreateTable(0, len(t))
		for k, item := range t {
			l.PushString(k)
			pushGo(l, item)
			l.SetTable(-3)
		}
	case time.Time:
		l.PushString(t.UTC().Format(time.RFC3339))
	case error:
		l.PushString(t.Error())
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(t, &decoded); err != nil {
			l.PushString(fmt.Sprintf("<unparseable: %v>", err))
			return
		}
		pushGo(l, decoded)
	default:
		// A nil or empty slice/map becomes an empty Lua table, not nil: JSON
		// would turn it into null, and scripts expect #list and pairs() to
		// work on an empty result.
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map:
			if rv.Len() == 0 {
				l.CreateTable(0, 0)
				return
			}
		}
		// Fallback: JSON round-trip so structs with json tags work.
		data, err := json.Marshal(v)
		if err != nil {
			l.PushString(fmt.Sprintf("<unserializable %T>", v))
			return
		}
		var decoded any
		if err := json.Unmarshal(data, &decoded); err != nil {
			l.PushString(string(data))
			return
		}
		pushGo(l, decoded)
	}
}
