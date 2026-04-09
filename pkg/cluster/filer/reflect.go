package filer

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// reflectAssign copies values from cfg into the struct pointed to by into.
// Field matching is case-insensitive and uses either the exact Go field
// name or a struct tag of the form `filer:"name"`. The `type` key is
// always skipped because it is consumed by FromConfig.
//
// Supported field types: string, bool, int, int64, []string, and
// map[string]string. Unknown keys produce an error so typos surface
// early during validation.
func reflectAssign(cfg map[string]interface{}, into interface{}) error {
	v := reflect.ValueOf(into)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return fmt.Errorf("reflectAssign: destination must be a non-nil pointer")
	}
	s := v.Elem()
	if s.Kind() != reflect.Struct {
		return fmt.Errorf("reflectAssign: destination must point to a struct")
	}
	t := s.Type()

	// Build a lookup of lowercase key -> field index.
	fieldByKey := map[string]int{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := strings.ToLower(f.Name)
		if tag := f.Tag.Get("filer"); tag != "" {
			name = strings.ToLower(strings.Split(tag, ",")[0])
		}
		fieldByKey[name] = i
	}

	for k, raw := range cfg {
		lk := strings.ToLower(k)
		if lk == "type" {
			continue
		}
		idx, ok := fieldByKey[lk]
		if !ok {
			return fmt.Errorf("unknown config key %q for backend %s", k, t.Name())
		}
		fv := s.Field(idx)
		if err := assignValue(fv, raw); err != nil {
			return fmt.Errorf("config key %q: %w", k, err)
		}
	}
	return nil
}

func assignValue(dst reflect.Value, raw interface{}) error {
	if raw == nil {
		return nil
	}
	switch dst.Kind() {
	case reflect.String:
		s, err := toString(raw)
		if err != nil {
			return err
		}
		dst.SetString(s)
	case reflect.Bool:
		b, err := toBool(raw)
		if err != nil {
			return err
		}
		dst.SetBool(b)
	case reflect.Int, reflect.Int64, reflect.Int32:
		i, err := toInt64(raw)
		if err != nil {
			return err
		}
		dst.SetInt(i)
	case reflect.Slice:
		if dst.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported slice element type %s", dst.Type().Elem())
		}
		vs, err := toStringSlice(raw)
		if err != nil {
			return err
		}
		dst.Set(reflect.ValueOf(vs))
	case reflect.Map:
		if dst.Type().Key().Kind() != reflect.String || dst.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported map type %s", dst.Type())
		}
		m, err := toStringMap(raw)
		if err != nil {
			return err
		}
		dst.Set(reflect.ValueOf(m))
	default:
		return fmt.Errorf("unsupported field kind %s", dst.Kind())
	}
	return nil
}

func toString(raw interface{}) (string, error) {
	switch v := raw.(type) {
	case string:
		return v, nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(v), nil
	}
	return "", fmt.Errorf("cannot convert %T to string", raw)
}

func toBool(raw interface{}) (bool, error) {
	switch v := raw.(type) {
	case bool:
		return v, nil
	case string:
		return strconv.ParseBool(v)
	}
	return false, fmt.Errorf("cannot convert %T to bool", raw)
}

func toInt64(raw interface{}) (int64, error) {
	switch v := raw.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case int32:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case string:
		return strconv.ParseInt(v, 10, 64)
	}
	return 0, fmt.Errorf("cannot convert %T to int64", raw)
}

func toStringSlice(raw interface{}) ([]string, error) {
	switch v := raw.(type) {
	case []string:
		return v, nil
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, err := toString(item)
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, nil
	case string:
		parts := strings.Split(v, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts, nil
	}
	return nil, fmt.Errorf("cannot convert %T to []string", raw)
}

func toStringMap(raw interface{}) (map[string]string, error) {
	switch v := raw.(type) {
	case map[string]string:
		return v, nil
	case map[string]interface{}:
		out := make(map[string]string, len(v))
		for k, val := range v {
			s, err := toString(val)
			if err != nil {
				return nil, err
			}
			out[k] = s
		}
		return out, nil
	case map[interface{}]interface{}:
		out := make(map[string]string, len(v))
		for k, val := range v {
			ks, err := toString(k)
			if err != nil {
				return nil, err
			}
			s, err := toString(val)
			if err != nil {
				return nil, err
			}
			out[ks] = s
		}
		return out, nil
	}
	return nil, fmt.Errorf("cannot convert %T to map[string]string", raw)
}
