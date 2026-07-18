package jsonx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type Object map[string]json.RawMessage

func Decode(data []byte) (Object, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var object Object
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	return object, nil
}

func (o Object) Raw(names ...string) (json.RawMessage, bool) {
	for _, name := range names {
		if value, ok := o[name]; ok {
			return value, true
		}
	}
	for key, value := range o {
		for _, name := range names {
			if strings.EqualFold(key, name) {
				return value, true
			}
		}
	}
	return nil, false
}

func (o Object) String(names ...string) string {
	raw, ok := o.Raw(names...)
	if !ok || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return number.String()
	}
	var boolean bool
	if err := json.Unmarshal(raw, &boolean); err == nil {
		return strconv.FormatBool(boolean)
	}
	return ""
}

func (o Object) Int64(names ...string) (int64, error) {
	value := o.String(names...)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q as integer: %w", value, err)
	}
	return parsed, nil
}

func (o Object) Bool(names ...string) (bool, error) {
	value := o.String(names...)
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %q as bool: %w", value, err)
	}
	return parsed, nil
}

func (o Object) Object(names ...string) (Object, bool, error) {
	raw, ok := o.Raw(names...)
	if !ok || bytes.Equal(raw, []byte("null")) {
		return nil, false, nil
	}
	var value Object
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (o Object) Array(names ...string) ([]Object, bool, error) {
	raw, ok := o.Raw(names...)
	if !ok || bytes.Equal(raw, []byte("null")) {
		return nil, false, nil
	}
	var value []Object
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, true, nil
	}

	// Some generated cloud APIs wrap arrays as {"Item": [...]},
	// {"Records": [...]} or {"List": [...]}.
	var wrapper Object
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, false, err
	}
	for _, nested := range [][]string{{"Item", "Items"}, {"Record", "Records"}, {"List"}, {"DetailSet"}} {
		if array, found, err := wrapper.Array(nested...); found || err != nil {
			return array, found, err
		}
	}
	return nil, false, fmt.Errorf("field %v is not an object array", names)
}

func (o Object) StringMap(names ...string) map[string]string {
	raw, ok := o.Raw(names...)
	if !ok || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var direct map[string]string
	if err := json.Unmarshal(raw, &direct); err == nil {
		return direct
	}
	var object Object
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil
	}
	result := make(map[string]string, len(object))
	for key := range object {
		result[key] = object.String(key)
	}
	return result
}
