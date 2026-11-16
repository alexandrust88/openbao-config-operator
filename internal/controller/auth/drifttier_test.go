package auth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

func TestAuthKindsAreAccessCritical(t *testing.T) {
	adapters := map[string]any{
		"AppRoleAdapter":                AppRoleAdapter{},
		"AppRoleSecretIDAdapter":        AppRoleSecretIDAdapter{},
		"AWSAuthCertificateAdapter":     AWSAuthCertificateAdapter{},
		"AWSAuthConfigAdapter":          AWSAuthConfigAdapter{},
		"AWSAuthIdentityConfigAdapter":  AWSAuthIdentityConfigAdapter{},
		"AWSAuthRoleAdapter":            AWSAuthRoleAdapter{},
		"AWSAuthSTSRoleAdapter":         AWSAuthSTSRoleAdapter{},
		"AWSAuthTidyConfigAdapter":      AWSAuthTidyConfigAdapter{},
		"AzureAuthConfigAdapter":        AzureAuthConfigAdapter{},
		"AzureAuthRoleAdapter":          AzureAuthRoleAdapter{},
		"CertAuthConfigAdapter":         CertAuthConfigAdapter{},
		"CertAuthCRLAdapter":            CertAuthCRLAdapter{},
		"CertAuthRoleAdapter":           CertAuthRoleAdapter{},
		"CFAuthConfigAdapter":           CFAuthConfigAdapter{},
		"CFAuthRoleAdapter":             CFAuthRoleAdapter{},
		"GCPAuthConfigAdapter":          GCPAuthConfigAdapter{},
		"GCPAuthRoleAdapter":            GCPAuthRoleAdapter{},
		"GitHubAuthConfigAdapter":       GitHubAuthConfigAdapter{},
		"GitHubAuthTeamAdapter":         GitHubAuthTeamAdapter{},
		"GitHubAuthUserAdapter":         GitHubAuthUserAdapter{},
		"JWTAuthConfigAdapter":          JWTAuthConfigAdapter{},
		"JWTAuthRoleAdapter":            JWTAuthRoleAdapter{},
		"JWTCELRoleAdapter":             JWTCELRoleAdapter{},
		"KerberosAuthConfigAdapter":     KerberosAuthConfigAdapter{},
		"KerberosAuthGroupAdapter":      KerberosAuthGroupAdapter{},
		"KerberosAuthLDAPConfigAdapter": KerberosAuthLDAPConfigAdapter{},
		"KubernetesAuthConfigAdapter":   KubernetesAuthConfigAdapter{},
		"KubernetesAuthRoleAdapter":     KubernetesAuthRoleAdapter{},
		"LDAPAuthConfigAdapter":         LDAPAuthConfigAdapter{},
		"LDAPAuthGroupAdapter":          LDAPAuthGroupAdapter{},
		"LDAPAuthUserAdapter":           LDAPAuthUserAdapter{},
		"OCIAuthConfigAdapter":          OCIAuthConfigAdapter{},
		"OCIAuthRoleAdapter":            OCIAuthRoleAdapter{},
		"OktaAuthConfigAdapter":         OktaAuthConfigAdapter{},
		"OktaAuthGroupAdapter":          OktaAuthGroupAdapter{},
		"OktaAuthUserAdapter":           OktaAuthUserAdapter{},
		"RADIUSAuthConfigAdapter":       RADIUSAuthConfigAdapter{},
		"RADIUSAuthUserAdapter":         RADIUSAuthUserAdapter{},
		"TokenRoleAdapter":              TokenRoleAdapter{},
		"UserpassUserAdapter":           UserpassUserAdapter{},
	}
	for name, a := range adapters {
		tier, ok := a.(engine.DriftTier)
		if !ok || tier.DriftTier() != engine.TierAccessCritical {
			t.Errorf("%s: want engine.DriftTier reporting %s", name, engine.TierAccessCritical)
		}
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				name := spec.(*ast.TypeSpec).Name.Name
				if _, listed := adapters[name]; strings.HasSuffix(name, "Adapter") && !listed {
					t.Errorf("%s is not listed here; every auth kind must report %s", name, engine.TierAccessCritical)
				}
			}
		}
	}
}
