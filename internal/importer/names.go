package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// maxNameLength keeps generated names well inside the 253 characters of a DNS subdomain.
const maxNameLength = 200

// maxLabelLength is the RFC 1123 limit of one dot-separated label.
const maxLabelLength = 63

// objectName joins the non-empty parts into a DNS-1123 subdomain: lower case, empty labels dropped, every other
// character replaced by "-". A name that has to be shortened keeps a hash of the full name so it stays unique.
func objectName(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	joined := strings.Join(kept, "-")
	var labels []string
	shortened := false
	for raw := range strings.SplitSeq(strings.ToLower(joined), ".") {
		label := dnsLabel(raw)
		if len(label) > maxLabelLength {
			label = truncate(label, maxLabelLength)
			shortened = true
		}
		if label != "" {
			labels = append(labels, label)
		}
	}
	name := strings.Join(labels, ".")
	if name == "" {
		return "imported"
	}
	if !shortened && len(name) <= maxNameLength {
		return name
	}
	sum := sha256.Sum256([]byte(joined))
	return withSuffix(name, "-"+hex.EncodeToString(sum[:4]))
}

// dnsLabel replaces every run of characters outside [a-z0-9] by one "-" and trims dashes at both ends.
func dnsLabel(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// withSuffix appends suffix to the last label of name, shortening name so that neither the label nor the name
// grows past its limit.
func withSuffix(name, suffix string) string {
	name = truncate(name, maxNameLength-len(suffix))
	if start := strings.LastIndexByte(name, '.') + 1; len(name)-start > maxLabelLength-len(suffix) {
		name = truncate(name, start+maxLabelLength-len(suffix))
	}
	return name + suffix
}

func truncate(name string, n int) string {
	if len(name) <= n {
		return name
	}
	return strings.TrimRight(name[:n], "-.")
}
