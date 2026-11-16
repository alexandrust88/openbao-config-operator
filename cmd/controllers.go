/*
Copyright 2026 The openbao-config-operator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	accessv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/access/v1alpha1"
	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	corev1alpha1 "github.com/alexandrust88/openbao-config-operator/api/core/v1alpha1"
	identityv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/identity/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	sysv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/sys/v1alpha1"
	accesscontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/access"
	authcontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/auth"
	corecontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/core"
	identitycontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/identity"
	secretscontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/secrets"
	syscontroller "github.com/alexandrust88/openbao-config-operator/internal/controller/sys"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
)

// apiSchemes registers every API group the operator serves.
var apiSchemes = []func(*runtime.Scheme) error{
	corev1alpha1.AddToScheme,
	sysv1alpha1.AddToScheme,
	authv1alpha1.AddToScheme,
	identityv1alpha1.AddToScheme,
	secretsv1alpha1.AddToScheme,
	accessv1alpha1.AddToScheme,
}

// kindController is the Setup func of one managed kind. highVolume kinds usually have many objects and get
// more concurrent reconciles.
type kindController struct {
	name       string
	setup      func(ctrl.Manager, engine.Connections, engine.Options) error
	highVolume bool
}

// kindControllers lists the kinds built into OpenBao.
var kindControllers = []kindController{
	{"core/OpenBaoEndpoint", corecontroller.SetupOpenBaoEndpoint, false},
	{"sys/Audit", syscontroller.SetupAudit, false},
	{"sys/AuditRequestHeader", syscontroller.SetupAuditRequestHeader, false},
	{"sys/AuthMount", syscontroller.SetupAuthMount, false},
	{"sys/CORSConfig", syscontroller.SetupCORSConfig, false},
	{"sys/KeyRotationConfig", syscontroller.SetupKeyRotationConfig, false},
	{"sys/Logger", syscontroller.SetupLogger, false},
	{"sys/OpenBaoNamespace", syscontroller.SetupOpenBaoNamespace, false},
	{"sys/PasswordPolicy", syscontroller.SetupPasswordPolicy, false},
	{"sys/Plugin", syscontroller.SetupPlugin, false},
	{"sys/Policy", syscontroller.SetupPolicy, true},
	{"sys/QuotaConfig", syscontroller.SetupQuotaConfig, false},
	{"sys/RaftAutopilot", syscontroller.SetupRaftAutopilot, false},
	{"sys/RateLimitQuota", syscontroller.SetupRateLimitQuota, false},
	{"sys/SecretsMount", syscontroller.SetupSecretsMount, false},
	{"sys/UIHeader", syscontroller.SetupUIHeader, false},
	{"sys/Workflow", syscontroller.SetupWorkflow, false},
	{"auth/AppRole", authcontroller.SetupAppRole, true},
	{"auth/AppRoleSecretID", authcontroller.SetupAppRoleSecretID, false},
	{"auth/CertAuthConfig", authcontroller.SetupCertAuthConfig, false},
	{"auth/CertAuthRole", authcontroller.SetupCertAuthRole, true},
	{"auth/CertAuthCRL", authcontroller.SetupCertAuthCRL, false},
	{"auth/JWTAuthConfig", authcontroller.SetupJWTAuthConfig, false},
	{"auth/JWTAuthRole", authcontroller.SetupJWTAuthRole, true},
	{"auth/JWTCELRole", authcontroller.SetupJWTCELRole, true},
	{"auth/KubernetesAuthConfig", authcontroller.SetupKubernetesAuthConfig, false},
	{"auth/KubernetesAuthRole", authcontroller.SetupKubernetesAuthRole, true},
	{"auth/TokenRole", authcontroller.SetupTokenRole, true},
	{"auth/UserpassUser", authcontroller.SetupUserpassUser, false},
	{"identity/Entity", identitycontroller.SetupEntity, true},
	{"identity/EntityAlias", identitycontroller.SetupEntityAlias, true},
	{"identity/Group", identitycontroller.SetupGroup, true},
	{"identity/GroupAlias", identitycontroller.SetupGroupAlias, true},
	{"identity/GroupMembership", identitycontroller.SetupGroupMembership, true},
	{"identity/MFALoginEnforcement", identitycontroller.SetupMFALoginEnforcement, false},
	{"identity/MFAMethod", identitycontroller.SetupMFAMethod, false},
	{"identity/OIDCAssignment", identitycontroller.SetupOIDCAssignment, false},
	{"identity/OIDCClient", identitycontroller.SetupOIDCClient, false},
	{"identity/OIDCConfig", identitycontroller.SetupOIDCConfig, false},
	{"identity/OIDCKey", identitycontroller.SetupOIDCKey, false},
	{"identity/OIDCProvider", identitycontroller.SetupOIDCProvider, false},
	{"identity/OIDCRole", identitycontroller.SetupOIDCRole, true},
	{"identity/OIDCScope", identitycontroller.SetupOIDCScope, false},
	{"secrets/DatabaseConnection", secretscontroller.SetupDatabaseConnection, false},
	{"secrets/DatabaseRole", secretscontroller.SetupDatabaseRole, true},
	{"secrets/DatabaseStaticRole", secretscontroller.SetupDatabaseStaticRole, true},
	{"secrets/KVConfig", secretscontroller.SetupKVConfig, false},
	{"secrets/KVSecret", secretscontroller.SetupKVSecret, false},
	{"secrets/KVSecretMetadata", secretscontroller.SetupKVSecretMetadata, false},
	{"secrets/KubernetesSecretsConfig", secretscontroller.SetupKubernetesSecretsConfig, false},
	{"secrets/KubernetesSecretsRole", secretscontroller.SetupKubernetesSecretsRole, true},
	{"secrets/PKIACMEConfig", secretscontroller.SetupPKIACMEConfig, false},
	{"secrets/PKIAutoTidy", secretscontroller.SetupPKIAutoTidy, false},
	{"secrets/PKIConfig", secretscontroller.SetupPKIConfig, false},
	{"secrets/PKIIssuer", secretscontroller.SetupPKIIssuer, false},
	{"secrets/PKIKey", secretscontroller.SetupPKIKey, false},
	{"secrets/PKIRole", secretscontroller.SetupPKIRole, true},
	{"secrets/PKICELRole", secretscontroller.SetupPKICELRole, true},
	{"secrets/PKIKeyConfig", secretscontroller.SetupPKIKeyConfig, false},
	{"secrets/RabbitMQConfig", secretscontroller.SetupRabbitMQConfig, false},
	{"secrets/RabbitMQRole", secretscontroller.SetupRabbitMQRole, true},
	{"secrets/RandomSecret", secretscontroller.SetupRandomSecret, false},
	{"secrets/SSHCA", secretscontroller.SetupSSHCA, false},
	{"secrets/SSHRole", secretscontroller.SetupSSHRole, true},
	{"secrets/TOTPKey", secretscontroller.SetupTOTPKey, false},
	{"secrets/TransitCacheConfig", secretscontroller.SetupTransitCacheConfig, false},
	{"secrets/TransitKey", secretscontroller.SetupTransitKey, false},
	{"secrets/TransitKeyConfig", secretscontroller.SetupTransitKeyConfig, false},
	{"access/AccessGrant", accesscontroller.SetupAccessGrant, false},
	{"access/AccessLayout", accesscontroller.SetupAccessLayout, false},
	{"access/AccessLevel", accesscontroller.SetupAccessLevel, false},
}

// pluginKindControllers lists the kinds that need an external OpenBao plugin; started only with
// --enable-plugin-kinds. hack/chart/plugin-crds.txt names their CRDs.
var pluginKindControllers = []kindController{
	{"auth/AWSAuthCertificate", authcontroller.SetupAWSAuthCertificate, false},
	{"auth/AWSAuthConfig", authcontroller.SetupAWSAuthConfig, false},
	{"auth/AWSAuthIdentityConfig", authcontroller.SetupAWSAuthIdentityConfig, false},
	{"auth/AWSAuthRole", authcontroller.SetupAWSAuthRole, true},
	{"auth/AWSAuthSTSRole", authcontroller.SetupAWSAuthSTSRole, true},
	{"auth/AWSAuthTidyConfig", authcontroller.SetupAWSAuthTidyConfig, false},
	{"auth/AzureAuthConfig", authcontroller.SetupAzureAuthConfig, false},
	{"auth/AzureAuthRole", authcontroller.SetupAzureAuthRole, true},
	{"auth/CFAuthConfig", authcontroller.SetupCFAuthConfig, false},
	{"auth/CFAuthRole", authcontroller.SetupCFAuthRole, true},
	{"auth/GCPAuthConfig", authcontroller.SetupGCPAuthConfig, false},
	{"auth/GCPAuthRole", authcontroller.SetupGCPAuthRole, true},
	{"auth/GitHubAuthConfig", authcontroller.SetupGitHubAuthConfig, false},
	{"auth/GitHubAuthTeam", authcontroller.SetupGitHubAuthTeam, false},
	{"auth/GitHubAuthUser", authcontroller.SetupGitHubAuthUser, false},
	{"auth/KerberosAuthConfig", authcontroller.SetupKerberosAuthConfig, false},
	{"auth/KerberosAuthGroup", authcontroller.SetupKerberosAuthGroup, true},
	{"auth/KerberosAuthLDAPConfig", authcontroller.SetupKerberosAuthLDAPConfig, false},
	{"auth/LDAPAuthConfig", authcontroller.SetupLDAPAuthConfig, false},
	{"auth/LDAPAuthGroup", authcontroller.SetupLDAPAuthGroup, true},
	{"auth/LDAPAuthUser", authcontroller.SetupLDAPAuthUser, false},
	{"auth/OCIAuthConfig", authcontroller.SetupOCIAuthConfig, false},
	{"auth/OCIAuthRole", authcontroller.SetupOCIAuthRole, true},
	{"auth/OktaAuthConfig", authcontroller.SetupOktaAuthConfig, false},
	{"auth/OktaAuthGroup", authcontroller.SetupOktaAuthGroup, true},
	{"auth/OktaAuthUser", authcontroller.SetupOktaAuthUser, false},
	{"auth/RADIUSAuthConfig", authcontroller.SetupRADIUSAuthConfig, false},
	{"auth/RADIUSAuthUser", authcontroller.SetupRADIUSAuthUser, false},
	{"secrets/AWSSecretsConfig", secretscontroller.SetupAWSSecretsConfig, false},
	{"secrets/AWSSecretsRole", secretscontroller.SetupAWSSecretsRole, true},
	{"secrets/AzureSecretsConfig", secretscontroller.SetupAzureSecretsConfig, false},
	{"secrets/AzureSecretsRole", secretscontroller.SetupAzureSecretsRole, true},
	{"secrets/ConsulSecretsConfig", secretscontroller.SetupConsulSecretsConfig, false},
	{"secrets/ConsulSecretsRole", secretscontroller.SetupConsulSecretsRole, true},
	{"secrets/GCPImpersonatedAccount", secretscontroller.SetupGCPImpersonatedAccount, false},
	{"secrets/GCPRoleset", secretscontroller.SetupGCPRoleset, false},
	{"secrets/GCPSecretsConfig", secretscontroller.SetupGCPSecretsConfig, false},
	{"secrets/GCPStaticAccount", secretscontroller.SetupGCPStaticAccount, false},
	{"secrets/GitHubSecretsConfig", secretscontroller.SetupGitHubSecretsConfig, false},
	{"secrets/GitHubSecretsRole", secretscontroller.SetupGitHubSecretsRole, true},
	{"secrets/LDAPSecretsConfig", secretscontroller.SetupLDAPSecretsConfig, false},
	{"secrets/LDAPSecretsDynamicRole", secretscontroller.SetupLDAPSecretsDynamicRole, true},
	{"secrets/LDAPSecretsStaticRole", secretscontroller.SetupLDAPSecretsStaticRole, true},
	{"secrets/MongoDBAtlasSecretsConfig", secretscontroller.SetupMongoDBAtlasSecretsConfig, false},
	{"secrets/MongoDBAtlasSecretsRole", secretscontroller.SetupMongoDBAtlasSecretsRole, true},
	{"secrets/NomadSecretsConfig", secretscontroller.SetupNomadSecretsConfig, false},
	{"secrets/NomadSecretsRole", secretscontroller.SetupNomadSecretsRole, true},
	{"secrets/QuaySecretsConfig", secretscontroller.SetupQuaySecretsConfig, false},
	{"secrets/QuaySecretsRole", secretscontroller.SetupQuaySecretsRole, true},
	{"secrets/QuaySecretsStaticRole", secretscontroller.SetupQuaySecretsStaticRole, true},
	{"secrets/TerraformCloudSecretsConfig", secretscontroller.SetupTerraformCloudSecretsConfig, false},
	{"secrets/TerraformCloudSecretsRole", secretscontroller.SetupTerraformCloudSecretsRole, true},
}
