package secrets

import (
	"encoding/json"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// optBool, optInt32, optSeconds and optString add a key only when the spec field is set, so an unset
// field never reaches the payload as null or a zero value.

func optBool(p engine.Payload, key string, v *bool) {
	if v != nil {
		p[key] = *v
	}
}

func optInt32(p engine.Payload, key string, v *int32) {
	if v != nil {
		p[key] = int64(*v)
	}
}

func optSeconds(p engine.Payload, key string, v *metav1.Duration) {
	if v != nil {
		p[key] = int64(v.Seconds())
	}
}

func optString(p engine.Payload, key, v string) {
	if v != "" {
		p[key] = v
	}
}

// orFalse, orTrue, orInt32, orSeconds, orString, orCSV, orList, orListDefault and orMap set a key to the spec value or, when the field is
// unset, to the server default, so removing a field from the spec resets it in OpenBao.

func orFalse(p engine.Payload, key string, v *bool) {
	p[key] = false
	optBool(p, key, v)
}

func orTrue(p engine.Payload, key string, v *bool) {
	p[key] = true
	optBool(p, key, v)
}

func orInt32(p engine.Payload, key string, v *int32, def int64) {
	p[key] = def
	optInt32(p, key, v)
}

func orSeconds(p engine.Payload, key string, v *metav1.Duration, def int64) {
	p[key] = def
	optSeconds(p, key, v)
}

func orString(p engine.Payload, key, v, def string) {
	p[key] = def
	optString(p, key, v)
}

func orCSV(p engine.Payload, key string, v []string) {
	p[key] = strings.Join(v, ",")
}

func orList(p engine.Payload, key string, v []string) {
	if v == nil {
		v = []string{}
	}
	p[key] = v
}

func orListDefault(p engine.Payload, key string, v, def []string) {
	if v == nil {
		v = def
	}
	p[key] = v
}

func orMap[V any](p engine.Payload, key string, v map[string]V) {
	if v == nil {
		v = map[string]V{}
	}
	p[key] = v
}

// asInt64 converts a number decoded from an OpenBao response.
func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	}
	return 0, false
}

// specName returns the OpenBao name of an object: spec.name, or metadata.name when it is empty.
func specName(name, objectName string) string {
	if name != "" {
		return name
	}
	return objectName
}
