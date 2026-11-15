package importer

import (
	"net/url"
	"reflect"
	"regexp"
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

	dsnKey     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	pairKey    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 _.:-]*$`)
	assignment = regexp.MustCompile(`([A-Za-z0-9_]+)\s*=\s*`)
)

const spaces = " \t\r\n"

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

// redactConnectionString is redactURL for a field that holds a database connection string, which may also be a
// PostgreSQL keyword/value string or a semicolon-separated (ADO.NET, ODBC) one. A value that still assigns a literal
// credential after parsing, or whose user info cannot be delimited, is replaced whole.
func redactConnectionString(value string) (string, bool) {
	out, parsed := "", false
	if strings.Contains(value, ";") {
		out, parsed = redactPairs(value)
	}
	if !parsed {
		out, parsed = redactKeywordDSN(value)
	}
	if !parsed {
		if ambiguousUserinfo(value) {
			return redacted, true
		}
		out, _ = redactURL(value)
	}
	if literalCredential(out) {
		return redacted, true
	}
	return out, out != value
}

// connectionStringField reports whether path names a field that holds a connection string: connectionURL,
// connectionURI, or connection_url and url in pluginConfig.
func connectionStringField(path string) bool {
	name := norm(path[strings.LastIndex(path, ".")+1:])
	return strings.HasPrefix(name, "connection") || name == "url"
}

// ambiguousUserinfo reports a connection string whose user info redactURL cannot delimit: MySQL DSNs leave @, /, ?
// and # unescaped in a password, and redactURL skips a value with whitespace.
func ambiguousUserinfo(value string) bool {
	_, rest, ok := strings.Cut(value, "://")
	if !ok {
		rest = value
	}
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return false
	}
	if strings.ContainsAny(value, spaces) {
		return true
	}
	end := strings.IndexAny(rest, "/?#")
	return end >= 0 && end < at && strings.Contains(rest[:at], ":")
}

// redactKeywordDSN redacts a PostgreSQL keyword/value connection string such as host=db password='a b': key = value
// pairs separated by whitespace, each value single-quoted or not, with backslash escapes. ok is false when value is
// not one.
func redactKeywordDSN(value string) (string, bool) {
	var b strings.Builder
	rest := strings.TrimLeft(value, spaces)
	b.WriteString(value[:len(value)-len(rest)])
	for rest != "" {
		eq := strings.IndexByte(rest, '=')
		if eq < 0 || !dsnKey.MatchString(strings.TrimRight(rest[:eq], spaces)) {
			return "", false
		}
		v := strings.TrimLeft(rest[eq+1:], spaces)
		n := keywordValueLen(v)
		if n < 0 {
			return "", false
		}
		b.WriteString(rest[:len(rest)-len(v)])
		writeValue(&b, rest[:eq], v[:n])
		next := strings.TrimLeft(v[n:], spaces)
		b.WriteString(v[n : len(v)-len(next)])
		rest = next
	}
	return b.String(), true
}

func keywordValueLen(v string) int {
	quoted := strings.HasPrefix(v, "'")
	i := 0
	if quoted {
		i = 1
	}
	for ; i < len(v); i++ {
		switch c := v[i]; {
		case c == '\\':
			i++
		case quoted && c == '\'':
			return i + 1
		case !quoted && strings.IndexByte(spaces, c) >= 0:
			return i
		}
	}
	if quoted {
		return -1
	}
	return len(v)
}

// redactPairs redacts a semicolon-separated connection string such as server=db;Password="a;b", as mssql accepts in
// ADO.NET and ODBC form: a value may be quoted with ' or " (doubled to escape) or with {} (}} to escape). ok is false
// when value is not one.
func redactPairs(value string) (string, bool) {
	var b strings.Builder
	rest := value
	for {
		next := strings.TrimLeft(rest, spaces+";")
		b.WriteString(rest[:len(rest)-len(next)])
		if rest = next; rest == "" {
			return b.String(), true
		}
		eq := strings.IndexByte(rest, '=')
		if eq < 0 || !pairKey.MatchString(strings.TrimSpace(rest[:eq])) {
			return "", false
		}
		v := rest[eq+1:]
		n := pairValueLen(v)
		if n < 0 {
			return "", false
		}
		b.WriteString(rest[:eq+1])
		writeValue(&b, rest[:eq], v[:n])
		rest = v[n:]
	}
}

func pairValueLen(v string) int {
	i := len(v) - len(strings.TrimLeft(v, spaces))
	if i == len(v) {
		return i
	}
	var closing byte
	switch v[i] {
	case '\'', '"':
		closing = v[i]
	case '{':
		closing = '}'
	default:
		if end := strings.IndexByte(v, ';'); end >= 0 {
			return end
		}
		return len(v)
	}
	for j := i + 1; j < len(v); j++ {
		if v[j] != closing {
			continue
		}
		if j+1 < len(v) && v[j+1] == closing {
			j++
			continue
		}
		after := strings.TrimLeft(v[j+1:], spaces)
		if after != "" && after[0] != ';' {
			return -1
		}
		return len(v) - len(after)
	}
	return -1
}

func writeValue(b *strings.Builder, key, value string) {
	if credentialParam(strings.TrimSpace(key)) && !kept(strings.Trim(value, spaces+`'"`)) {
		b.WriteString(redacted)
		return
	}
	b.WriteString(value)
}

// literalCredential reports a credential assignment left in s whose value is not kept.
func literalCredential(s string) bool {
	for _, m := range assignment.FindAllStringSubmatchIndex(s, -1) {
		if !credentialParam(s[m[2]:m[3]]) {
			continue
		}
		value := s[m[1]:]
		if end := strings.IndexAny(value, spaces+";&#"); end >= 0 {
			value = value[:end]
		}
		if !kept(strings.Trim(value, `'"`)) {
			return true
		}
	}
	return false
}

// kept reports a credential value the import may emit: empty, already redacted, the xxxxx OpenBao returns in place
// of a password, or a template.
func kept(value string) bool {
	return value == "" || value == redacted || value == "xxxxx" || template(value)
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
		redact := redactURL
		if connectionStringField(path) {
			redact = redactConnectionString
		}
		out, ok := redact(value)
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
