package importer

import (
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

const (
	// redacted replaces a credential the import must not emit.
	redacted    = "REDACTED"
	keyPassword = "password"
)

// credentialParams are query parameter names, folded with norm, that carry a credential; credentialSuffixes also
// match names such as sslpassword or X-Amz-Signature.
var (
	credentialParams   = []string{"auth", "key", "pwd", "sig", "accesstoken", "authtoken", "clientsecret"}
	credentialSuffixes = []string{"token", keyPassword, "passwd", "secret", "signature", "apikey"}
)

// redactURL returns value with the credentials of a URL replaced by REDACTED: a userinfo password, a userinfo that
// is only a user name (a token), and the values of credential query parameters. OpenBao templates such as
// {{password}} and the xxxxx OpenBao itself puts in place of a password are kept. ok is false when value is not a
// URL or carries no credential.
func redactURL(value string) (string, bool) {
	if strings.ContainsAny(value, " \t\r\n") {
		return value, false
	}
	scheme, rest, hasScheme := strings.Cut(value, "://")
	if !hasScheme || scheme == "" || strings.ContainsAny(scheme, "/?#@") {
		scheme, rest, hasScheme = "", value, false
	}
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	authority, tail := rest[:end], rest[end:]
	at := strings.LastIndex(authority, "@")
	userinfo := ""
	if at >= 0 {
		userinfo = authority[:at]
	}
	user, password, hasPassword := strings.Cut(userinfo, ":")
	if !hasScheme && !hasPassword {
		return value, false
	}
	changed := false
	switch {
	case hasPassword && password != "" && password != "xxxxx" && !template(password):
		userinfo, changed = user+":"+redacted, true
	case !hasPassword && user != "" && !template(user):
		userinfo, changed = redacted, true
	}
	tail, queryChanged := redactQuery(tail)
	if !changed && !queryChanged {
		return value, false
	}
	if at >= 0 {
		authority = userinfo + authority[at:]
	}
	if hasScheme {
		return scheme + "://" + authority + tail, true
	}
	return authority + tail, true
}

// redactQuery replaces the values of credential parameters in the query of tail, a URL path with query and
// fragment; the other parameters are kept as written.
func redactQuery(tail string) (string, bool) {
	path, query, ok := strings.Cut(tail, "?")
	if !ok {
		return tail, false
	}
	query, fragment, hasFragment := strings.Cut(query, "#")
	params := strings.Split(query, "&")
	changed := false
	for i, param := range params {
		name, value, _ := strings.Cut(param, "=")
		if unescaped, err := url.QueryUnescape(name); err == nil {
			name = unescaped
		}
		if value == "" || template(value) || !credentialParam(name) {
			continue
		}
		params[i] = param[:strings.Index(param, "=")+1] + redacted
		changed = true
	}
	tail = path + "?" + strings.Join(params, "&")
	if hasFragment {
		tail += "#" + fragment
	}
	return tail, changed
}

func credentialParam(name string) bool {
	folded := norm(name)
	return slices.Contains(credentialParams, folded) ||
		slices.ContainsFunc(credentialSuffixes, func(s string) bool { return strings.HasSuffix(folded, s) })
}

func template(s string) bool {
	return strings.HasPrefix(s, "{{") && strings.HasSuffix(s, "}}")
}

// redactURLs replaces the credentials of every URL in the spec of obj that no fill moved into a Secret reference,
// and notes each field, so the emitted YAML never holds them.
func (s *scope) redactURLs(obj engine.Object) {
	walkStrings(specValue(obj), "spec", func(path, value string) (string, bool) {
		out, ok := redactURL(value)
		if ok {
			s.note("%s: %s held credentials in a URL, emitted as %s: set the real value before managementPolicy Manage",
				s.label(obj), path, redacted)
		}
		return out, ok
	})
}

// walkStrings calls replace with every string in v, a spec value, and stores the replacement when ok.
func walkStrings(v reflect.Value, path string, replace func(path, value string) (string, bool)) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			walkStrings(v.Elem(), path, replace)
		}
	case reflect.Struct:
		for _, f := range fields(v) {
			walkStrings(f.value, path+"."+f.name, replace)
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			walkStrings(v.Index(i), path+"["+strconv.Itoa(i)+"]", replace)
		}
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return
		}
		for _, key := range v.MapKeys() {
			elem := reflect.New(v.Type().Elem()).Elem()
			elem.Set(v.MapIndex(key))
			walkStrings(elem, path+"."+key.String(), replace)
			v.SetMapIndex(key, elem)
		}
	case reflect.String:
		if out, ok := replace(path, v.String()); ok && v.CanSet() {
			v.SetString(out)
		}
	default:
	}
}
