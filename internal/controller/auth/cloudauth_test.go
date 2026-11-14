package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	authv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/auth/v1alpha1"
	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

const (
	cloudAccessKey                = "AKIAFAKEACCESSKEY"
	cloudCIDR                     = "10.0.0.0/8"
	cloudHeaderA                  = "X-Custom-A"
	cloudKeyAccessKey             = "access_key"
	cloudAccountIDField           = "account_id"
	cloudAMI                      = "ami-0123456789abcdef0"
	cloudTenantID                 = "00000000-0000-0000-0000-000000000001"
	cloudClientID                 = "00000000-0000-0000-0000-000000000002"
	cloudKeyAPI                   = "api"
	cloudInstanceGroup            = "app-group"
	cloudKeyAllowGCEInference     = "allow_gce_inference"
	cloudAppID                    = "app-1"
	cloudLitAzurePublicCloud      = "AzurePublicCloud"
	cloudLitXCustomB              = "X-Custom-B"
	cloudSAEmail                  = "app@project.iam.gserviceaccount.com"
	cloudARNInstanceProfile       = "arn:aws:iam::123456789012:instance-profile/app*"
	cloudARNRoleApp               = "arn:aws:iam::123456789012:role/app"
	cloudARNRoleVerify            = "arn:aws:iam::123456789012:role/openbao-verify"
	cloudARNRoleOps               = "arn:aws:iam::123456789012:role/ops"
	cloudLitAuthType              = "auth_type"
	cloudLitBoundServiceAccounts  = "bound_service_accounts"
	cloudLitClientID              = "client_id"
	cloudLitDisablePeriodicTidy   = "disable_periodic_tidy"
	cloudLitEC2Instance           = "ec2_instance"
	cloudLitEnv                   = "env"
	cloudLitEuCentral1            = "eu-central-1"
	cloudLitEuropeWest3           = "europe-west3"
	cloudLitEuropeWest3A          = "europe-west3-a"
	cloudLitGroup1                = "group-1"
	cloudLitGCE                   = "gce"
	cloudCFAPIAddr                = "https://api.cf.example"
	cloudGCPComputeEndpoint       = "https://compute.googleapis.example"
	cloudEC2Endpoint              = "https://ec2.eu-central-1.amazonaws.com"
	cloudIAMEndpoint              = "https://iam.amazonaws.com"
	cloudGCPIAMEndpoint           = "https://iam.googleapis.example"
	cloudAzureResource            = "https://management.azure.com/"
	cloudSTSEndpoint              = "https://sts.eu-central-1.amazonaws.com"
	cloudEC2InstanceID            = "i-0123456789abcdef0"
	cloudLitIAM                   = "iam"
	cloudLitIAMAlias              = "iam_alias"
	cloudLitInst1                 = "inst-1"
	cloudLitInstanceID            = "instance_id"
	cloudLitCrmGoogleapisExample  = "https://crm.googleapis.example"
	cloudLitWwwGoogleapisExample  = "https://www.googleapis.example"
	cloudLitIAMMetadata           = "iam_metadata"
	cloudLitInst2                 = "inst-2"
	cloudLitMaxJwtExp             = "max_jwt_exp"
	cloudLitMaxRetries            = "max_retries"
	cloudLitObco                  = "obco"
	cloudLitOcid1DynamicgroupOc1B = "ocid1.dynamicgroup.oc1..b"
	cloudLitOcid1TenancyOc1Fake   = "ocid1.tenancy.oc1..fake"
	cloudLitOcid1GroupOc1A        = "ocid1.group.oc1..a"
	cloudLitOpenbao               = "openbao"
	cloudLitOpenbaoExampleCom     = "openbao.example.com"
	cloudLitOrg1                  = "org-1"
	cloudLitPkcs7                 = "pkcs7"
	cloudLitProject               = "project"
	cloudLitProjectID             = "project_id"
)

// cloudClient serves reads from data (deep-copied, since observers mutate them) and records writes.
type cloudClient struct {
	openbao.Client
	data    map[string]map[string]any
	readErr map[string]error
	writes  map[string]map[string]any
}

func (c *cloudClient) Read(_ context.Context, path string) (map[string]any, error) {
	if err, ok := c.readErr[path]; ok {
		return nil, err
	}
	d, ok := c.data[path]
	if !ok {
		return nil, openbao.ErrNotFound
	}
	raw, _ := json.Marshal(d)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out, nil
}

func (c *cloudClient) Write(_ context.Context, path string, data map[string]any) (map[string]any, error) {
	if c.writes == nil {
		c.writes = map[string]map[string]any{}
	}
	c.writes[path] = data
	return nil, nil
}

type cloudResolver map[string]string

func (r cloudResolver) SecretValue(_ context.Context, _ string, ref commonv1alpha1.SecretKeySelector) ([]byte, error) {
	if v, ok := r[ref.Name+"/"+ref.Key]; ok {
		return []byte(v), nil
	}
	return nil, fmt.Errorf("secret %s: %w", ref.Name, engine.ErrReferenceNotReady)
}

func (r cloudResolver) Object(context.Context, client.ObjectKey, client.Object) error { return nil }

var cloudSecrets = cloudResolver{
	"aws-creds/access-key": cloudAccessKey,
	"aws-creds/secret-key": "fake-secret-key",
	"azure-creds/secret":   "fake-client-secret",
	"gcp-creds/key.json":   `{"type":"service_account","private_key":"fake"}`,
	"cf-creds/password":    "fake-password",
	"cf-creds/mtls-key":    "-----BEGIN PRIVATE KEY-----\nfake\n-----END PRIVATE KEY-----",
}

// cloudSysAuth is the read of sys/auth with the default mount of every plugin enabled.
var cloudSysAuth = map[string]any{
	"aws/": map[string]any{authLitType: "aws"}, "azure/": map[string]any{authLitType: "azure"},
	"gcp/": map[string]any{authLitType: "gcp"}, "oci/": map[string]any{authLitType: "oci"}, "cf/": map[string]any{authLitType: "cf"},
}

func cloudDur(d time.Duration) *metav1.Duration { return &metav1.Duration{Duration: d} }

func cloudRef(name, key string) *commonv1alpha1.SecretKeySelector {
	return &commonv1alpha1.SecretKeySelector{Name: name, Key: key}
}

func cloudMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: pluginTestNamespace}
}

func cloudJSON(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// cloudTokenFields sets every token field; cloudTokenRead is the matching read shape with extra server keys.
var cloudTokenFields = authv1alpha1.TokenFields{
	TokenBaseFields: authv1alpha1.TokenBaseFields{
		TokenExplicitMaxTTL:  cloudDur(48 * time.Hour),
		TokenPeriod:          cloudDur(30 * time.Minute),
		TokenBoundCIDRs:      []string{cloudCIDR, "192.168.1.10/32"},
		TokenNumUses:         new(int32(5)),
		TokenType:            authv1alpha1.TokenTypeService,
		TokenNoDefaultPolicy: new(true),
	},
	TokenTTL:            cloudDur(time.Hour),
	TokenMaxTTL:         cloudDur(24 * time.Hour),
	TokenPolicies:       []string{authLitReader, authLitWriter},
	TokenStrictlyBindIP: new(false),
}

var cloudTokenWant = engine.Payload{
	keyTokenExplicitMaxTTL: 172800, keyTokenPeriod: 1800, keyTokenBoundCIDRs: []string{cloudCIDR, "192.168.1.10"},
	keyTokenNumUses: 5, keyTokenType: authLitService, keyTokenNoDefaultPolicy: true, keyTokenTTL: 3600,
	keyTokenMaxTTL: 86400, keyTokenPolicies: []string{authLitReader, authLitWriter}, keyTokenStrictlyBindIP: false,
}

var cloudTokenRead = map[string]any{
	keyTokenExplicitMaxTTL: 172800, keyTokenPeriod: 1800, keyTokenBoundCIDRs: []any{"192.168.1.10", cloudCIDR},
	keyTokenNumUses: 5, keyTokenType: authLitService, keyTokenNoDefaultPolicy: true, keyTokenTTL: 3600,
	keyTokenMaxTTL: 86400, keyTokenPolicies: []any{authLitWriter, authLitReader}, keyTokenStrictlyBindIP: false,
}

func cloudWithToken(m map[string]any, token map[string]any) map[string]any {
	out := maps.Clone(m)
	maps.Copy(out, token)
	return out
}

type cloudObservingAdapter[T engine.Object] interface {
	engine.Adapter[T]
	engine.Observer[T]
}

// cloudCheckGolden asserts the path, the exact desired payload, that the golden read gives no diff, and that a
// changed managed key in the read is reported.
func cloudCheckGolden[T engine.Object](t *testing.T, a cloudObservingAdapter[T], obj T, wantPath string, want engine.Payload,
	read map[string]any, changedKey string, changedValue any) {
	t.Helper()
	ctx := context.Background()
	if got := a.Path(obj); got != wantPath {
		t.Fatalf("path = %q, want %q", got, wantPath)
	}
	desired, err := a.Desired(ctx, cloudSecrets, obj)
	if err != nil {
		t.Fatal(err)
	}
	if got, w := cloudJSON(t, desired), cloudJSON(t, want); !reflect.DeepEqual(got, w) {
		t.Fatalf("desired =\n%v\nwant\n%v", got, w)
	}
	for _, key := range a.Fields().WriteOnly {
		if _, inRead := read[key]; inRead {
			t.Fatalf("golden read must not contain write-only key %s", key)
		}
	}

	c := &cloudClient{data: map[string]map[string]any{wantPath: read, pluginSysAuth: cloudSysAuth}}
	observed, found, err := a.Observe(ctx, c, obj)
	if err != nil || !found {
		t.Fatalf("observe: found=%v err=%v", found, err)
	}
	if differing := engine.Diff(desired, observed, a.Fields()); len(differing) != 0 {
		t.Fatalf("golden read differs on %v", differing)
	}

	tampered := maps.Clone(read)
	tampered[changedKey] = changedValue
	c.data[wantPath] = tampered
	observed, _, err = a.Observe(ctx, c, obj)
	if err != nil {
		t.Fatal(err)
	}
	if differing := engine.Diff(desired, observed, a.Fields()); !slices.Contains(differing, changedKey) {
		t.Fatalf("changed %s not reported, differing %v", changedKey, differing)
	}
}

func TestAWSAuthConfigGolden(t *testing.T) {
	obj := &authv1alpha1.AWSAuthConfig{ObjectMeta: cloudMeta("aws"), Spec: authv1alpha1.AWSAuthConfigSpec{
		MountPath:              "aws-prod",
		AccessKeySecretRef:     cloudRef("aws-creds", "access-key"),
		SecretKeySecretRef:     cloudRef("aws-creds", "secret-key"),
		EC2Endpoint:            cloudEC2Endpoint,
		IAMEndpoint:            cloudIAMEndpoint,
		STSEndpoint:            cloudSTSEndpoint,
		STSRegion:              cloudLitEuCentral1,
		UseSTSRegionFromClient: new(false),
		IAMServerIDHeaderValue: cloudLitOpenbaoExampleCom,
		AllowedSTSHeaderValues: []string{cloudHeaderA, cloudLitXCustomB},
		MaxRetries:             new(int32(-1)),
	}}
	want := engine.Payload{
		cloudKeyAccessKey: cloudAccessKey, "secret_key": "fake-secret-key",
		"endpoint": cloudEC2Endpoint, "iam_endpoint": cloudIAMEndpoint,
		"sts_endpoint": cloudSTSEndpoint, "sts_region": cloudLitEuCentral1,
		"use_sts_region_from_client": false, "iam_server_id_header_value": cloudLitOpenbaoExampleCom,
		"allowed_sts_header_values": []string{cloudHeaderA, cloudLitXCustomB}, cloudLitMaxRetries: -1,
	}
	read := map[string]any{
		cloudKeyAccessKey: cloudAccessKey, "endpoint": cloudEC2Endpoint,
		"iam_endpoint": cloudIAMEndpoint, "sts_endpoint": cloudSTSEndpoint,
		"sts_region": cloudLitEuCentral1, "use_sts_region_from_client": false,
		"iam_server_id_header_value": cloudLitOpenbaoExampleCom, "allowed_sts_header_values": []any{cloudLitXCustomB, cloudHeaderA},
		cloudLitMaxRetries: -1, "role_arn": "", "identity_token_audience": "", "identity_token_ttl": 0,
	}
	a := AWSAuthConfigAdapter{}
	cloudCheckGolden(t, a, obj, "auth/aws-prod/config/client", want, read, "sts_region", "us-east-1")
	if got := a.SecretRefs(obj); !slices.Equal(got, []string{"aws-creds", "aws-creds"}) {
		t.Errorf("secret refs = %v", got)
	}
}

func TestAWSAuthConfigSecretKeyIsWriteOnly(t *testing.T) {
	ctx := context.Background()
	a := AWSAuthConfigAdapter{}
	obj := &authv1alpha1.AWSAuthConfig{ObjectMeta: cloudMeta("aws"), Spec: authv1alpha1.AWSAuthConfigSpec{
		AccessKeySecretRef: cloudRef("aws-creds", "access-key"), SecretKeySecretRef: cloudRef("aws-creds", "secret-key"),
	}}
	desired, err := a.Desired(ctx, cloudSecrets, obj)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Path(obj); got != "auth/aws/config/client" {
		t.Errorf("default mount path = %q", got)
	}
	rotated := cloudResolver{"aws-creds/access-key": cloudAccessKey, "aws-creds/secret-key": "rotated"}
	desiredRotated, err := a.Desired(ctx, rotated, obj)
	if err != nil {
		t.Fatal(err)
	}
	if engine.WriteOnlyHash([]byte("k"), desired, a.Fields()) == engine.WriteOnlyHash([]byte("k"), desiredRotated, a.Fields()) {
		t.Error("rotating the secret key must change the write-only hash")
	}
	if differing := engine.Diff(desired, engine.Payload{cloudKeyAccessKey: cloudAccessKey}, a.Fields()); len(differing) != 0 {
		t.Errorf("a read without secret_key differs on %v", differing)
	}

	_, err = a.Desired(ctx, cloudResolver{}, obj)
	if !errors.Is(err, engine.ErrReferenceNotReady) {
		t.Fatalf("missing secret: err = %v", err)
	}
	if strings.Contains(err.Error(), "fake-secret-key") {
		t.Error("error leaks the secret value")
	}
}

func TestAWSAuthIdentityConfigGolden(t *testing.T) {
	obj := &authv1alpha1.AWSAuthIdentityConfig{ObjectMeta: cloudMeta("identity"), Spec: authv1alpha1.AWSAuthIdentityConfigSpec{
		IAMAlias: authLitUniqueID, IAMMetadata: []string{cloudAccountIDField, cloudLitAuthType},
		EC2Alias: cloudLitInstanceID, EC2Metadata: []string{authLitRegion, cloudAccountIDField},
	}}
	want := engine.Payload{
		cloudLitIAMAlias: authLitUniqueID, cloudLitIAMMetadata: []string{cloudAccountIDField, cloudLitAuthType},
		"ec2_alias": cloudLitInstanceID, "ec2_metadata": []string{authLitRegion, cloudAccountIDField},
	}
	read := map[string]any{
		cloudLitIAMAlias: authLitUniqueID, cloudLitIAMMetadata: []any{cloudLitAuthType, cloudAccountIDField},
		"ec2_alias": cloudLitInstanceID, "ec2_metadata": []any{cloudAccountIDField, authLitRegion},
	}
	cloudCheckGolden(t, AWSAuthIdentityConfigAdapter{}, obj, "auth/aws/config/identity", want, read, cloudLitIAMAlias, authLitRoleID)
}

const cloudFakeCert = "-----BEGIN CERTIFICATE-----\nMIIBfakecertificate\n-----END CERTIFICATE-----"

func TestAWSAuthCertificateGolden(t *testing.T) {
	obj := &authv1alpha1.AWSAuthCertificate{ObjectMeta: cloudMeta(cloudLitEuCentral1), Spec: authv1alpha1.AWSAuthCertificateSpec{
		Name: "eu-central-1-pkcs7", AWSPublicCert: cloudFakeCert, Type: cloudLitPkcs7,
	}}
	want := engine.Payload{"aws_public_cert": cloudFakeCert, authLitType: cloudLitPkcs7}
	read := map[string]any{"aws_public_cert": cloudFakeCert + "\n", authLitType: cloudLitPkcs7}
	a := AWSAuthCertificateAdapter{}
	cloudCheckGolden(t, a, obj, "auth/aws/config/certificate/eu-central-1-pkcs7", want, read, "aws_public_cert",
		"-----BEGIN CERTIFICATE-----\nother\n-----END CERTIFICATE-----")
	obj.Spec.Name = ""
	if got := a.Path(obj); got != "auth/aws/config/certificate/eu-central-1" {
		t.Errorf("default name path = %q", got)
	}
}

func TestAWSAuthSTSRoleGolden(t *testing.T) {
	obj := &authv1alpha1.AWSAuthSTSRole{ObjectMeta: cloudMeta("sts"), Spec: authv1alpha1.AWSAuthSTSRoleSpec{
		AccountID: "123456789012", STSRole: cloudARNRoleVerify, ExternalID: cloudLitObco,
	}}
	want := engine.Payload{"sts_role": cloudARNRoleVerify, "external_id": cloudLitObco}
	read := map[string]any{"sts_role": cloudARNRoleVerify, "external_id": cloudLitObco}
	cloudCheckGolden(t, AWSAuthSTSRoleAdapter{}, obj, "auth/aws/config/sts/123456789012", want, read, "external_id", "x")
}

func TestAWSAuthRoleGolden(t *testing.T) {
	obj := &authv1alpha1.AWSAuthRole{ObjectMeta: cloudMeta("ec2-app"), Spec: authv1alpha1.AWSAuthRoleSpec{
		AuthType:                    cloudLitIAM,
		BoundAMIIDs:                 []string{cloudAMI},
		BoundAccountIDs:             []string{"123456789012"},
		BoundRegions:                []string{cloudLitEuCentral1},
		BoundVPCIDs:                 []string{authLitVpc0123},
		BoundSubnetIDs:              []string{authLitSubnet0123},
		BoundIAMRoleARNs:            []string{cloudARNRoleApp},
		BoundIAMInstanceProfileARNs: []string{cloudARNInstanceProfile},
		BoundEC2InstanceIDs:         []string{cloudEC2InstanceID},
		BoundIAMPrincipalARNs:       []string{cloudARNRoleApp, cloudARNRoleOps},
		InferredEntityType:          cloudLitEC2Instance,
		InferredAWSRegion:           cloudLitEuCentral1,
		ResolveAWSUniqueIDs:         new(true),
		TokenFields:                 cloudTokenFields,
	}}
	want := cloudWithToken(engine.Payload{
		cloudLitAuthType: cloudLitIAM, "bound_ami_id": []string{cloudAMI}, "bound_account_id": []string{"123456789012"},
		"bound_region": []string{cloudLitEuCentral1}, "bound_vpc_id": []string{authLitVpc0123},
		"bound_subnet_id": []string{authLitSubnet0123}, "bound_iam_role_arn": []string{cloudARNRoleApp},
		"bound_iam_instance_profile_arn": []string{cloudARNInstanceProfile},
		"bound_ec2_instance_id":          []string{cloudEC2InstanceID},
		"bound_iam_principal_arn":        []string{cloudARNRoleApp, cloudARNRoleOps},
		"inferred_entity_type":           cloudLitEC2Instance, "inferred_aws_region": cloudLitEuCentral1,
		"resolve_aws_unique_ids": true,
	}, cloudTokenWant)
	read := cloudWithToken(map[string]any{
		cloudLitAuthType: cloudLitIAM, "bound_ami_id": []any{cloudAMI}, "bound_account_id": []any{"123456789012"},
		"bound_region": []any{cloudLitEuCentral1}, "bound_vpc_id": []any{authLitVpc0123}, "bound_subnet_id": []any{authLitSubnet0123},
		"bound_iam_role_arn":             []any{cloudARNRoleApp},
		"bound_iam_instance_profile_arn": []any{cloudARNInstanceProfile},
		"bound_ec2_instance_id":          []any{cloudEC2InstanceID},
		"bound_iam_principal_arn":        []any{cloudARNRoleOps, cloudARNRoleApp},
		"bound_iam_principal_id":         []any{"AROAFAKE1", "AROAFAKE2"},
		"inferred_entity_type":           cloudLitEC2Instance, "inferred_aws_region": cloudLitEuCentral1,
		"resolve_aws_unique_ids": true, authLitRoleID: "8d6f4c5e-0000-0000-0000-000000000000", "role_tag": "",
		"allow_instance_migration": false, "disallow_reauthentication": false,
	}, cloudTokenRead)
	a := AWSAuthRoleAdapter{}
	cloudCheckGolden(t, a, obj, "auth/aws/role/ec2-app", want, read, "bound_region", []any{"us-east-1"})

	ec2 := &authv1alpha1.AWSAuthRole{ObjectMeta: cloudMeta("ec2"), Spec: authv1alpha1.AWSAuthRoleSpec{
		AuthType: "ec2", RoleTag: "VaultRole", AllowInstanceMigration: new(true), DisallowReauthentication: new(false),
	}}
	desired, _ := a.Desired(context.Background(), nil, ec2)
	if got, w := cloudJSON(t, desired), cloudJSON(t, engine.Payload{
		cloudLitAuthType: "ec2", "role_tag": "VaultRole", "allow_instance_migration": true, "disallow_reauthentication": false,
	}); !reflect.DeepEqual(got, w) {
		t.Errorf("ec2 desired = %v", got)
	}
	defaulted, _ := a.Desired(context.Background(), nil, &authv1alpha1.AWSAuthRole{ObjectMeta: cloudMeta("x")})
	if defaulted[cloudLitAuthType] != cloudLitIAM || len(defaulted) != 1 {
		t.Errorf("unset fields must be absent and auth_type default to iam: %v", defaulted)
	}
}

func TestAWSAuthTidyConfig(t *testing.T) {
	ctx := context.Background()
	a := AWSAuthTidyConfigAdapter{}
	obj := &authv1alpha1.AWSAuthTidyConfig{ObjectMeta: cloudMeta("tidy"), Spec: authv1alpha1.AWSAuthTidyConfigSpec{
		IdentityAccessList: &authv1alpha1.AWSAuthTidySettings{SafetyBuffer: cloudDur(72 * time.Hour), DisablePeriodicTidy: new(false)},
		RoleTagDenyList:    &authv1alpha1.AWSAuthTidySettings{SafetyBuffer: cloudDur(24 * time.Hour), DisablePeriodicTidy: new(true)},
	}}
	want := engine.Payload{
		"identity_accesslist_safety_buffer": 259200, "identity_accesslist_disable_periodic_tidy": false,
		"roletag_denylist_safety_buffer": 86400, "roletag_denylist_disable_periodic_tidy": true,
	}
	desired, err := a.Desired(ctx, nil, obj)
	if err != nil {
		t.Fatal(err)
	}
	if got, w := cloudJSON(t, desired), cloudJSON(t, want); !reflect.DeepEqual(got, w) {
		t.Fatalf("desired = %v", got)
	}

	c := &cloudClient{data: map[string]map[string]any{pluginSysAuth: cloudSysAuth}}
	if _, found, err := a.Observe(ctx, c, obj); found || err != nil {
		t.Fatalf("nothing written yet: found=%v err=%v", found, err)
	}
	if _, err := a.Write(ctx, c, obj, desired, true); err != nil {
		t.Fatal(err)
	}
	wantWrites := map[string]map[string]any{
		"auth/aws/config/tidy/identity-accesslist": {authLitSafetyBuffer: int64(259200), cloudLitDisablePeriodicTidy: false},
		"auth/aws/config/tidy/roletag-denylist":    {authLitSafetyBuffer: int64(86400), cloudLitDisablePeriodicTidy: true},
	}
	if !reflect.DeepEqual(c.writes, wantWrites) {
		t.Fatalf("writes = %v", c.writes)
	}

	c.data["auth/aws/config/tidy/identity-accesslist"] = map[string]any{authLitSafetyBuffer: 259200, cloudLitDisablePeriodicTidy: false}
	c.data["auth/aws/config/tidy/roletag-denylist"] = map[string]any{authLitSafetyBuffer: 86400, cloudLitDisablePeriodicTidy: true}
	observed, found, err := a.Observe(ctx, c, obj)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if differing := engine.Diff(desired, observed, a.Fields()); len(differing) != 0 {
		t.Fatalf("golden read differs on %v", differing)
	}
	c.data["auth/aws/config/tidy/roletag-denylist"] = map[string]any{authLitSafetyBuffer: 3600, cloudLitDisablePeriodicTidy: true}
	observed, _, _ = a.Observe(ctx, c, obj)
	if differing := engine.Diff(desired, observed, a.Fields()); !slices.Equal(differing, []string{"roletag_denylist_safety_buffer"}) {
		t.Fatalf("differing = %v", differing)
	}

	only := &authv1alpha1.AWSAuthTidyConfig{ObjectMeta: cloudMeta("tidy"), Spec: authv1alpha1.AWSAuthTidyConfigSpec{
		RoleTagDenyList: &authv1alpha1.AWSAuthTidySettings{DisablePeriodicTidy: new(true)},
	}}
	c.writes = nil
	d, _ := a.Desired(ctx, nil, only)
	if _, err := a.Write(ctx, c, only, d, false); err != nil {
		t.Fatal(err)
	}
	if len(c.writes) != 1 || c.writes["auth/aws/config/tidy/roletag-denylist"] == nil {
		t.Fatalf("only the set section is written: %v", c.writes)
	}
}

func TestAzureAuthConfigGolden(t *testing.T) {
	obj := &authv1alpha1.AzureAuthConfig{ObjectMeta: cloudMeta("azure"), Spec: authv1alpha1.AzureAuthConfigSpec{
		TenantID: cloudTenantID, Resource: cloudAzureResource,
		Environment: cloudLitAzurePublicCloud, ClientID: cloudClientID,
		ClientSecretSecretRef: cloudRef("azure-creds", "secret"), MaxRetries: new(int32(3)),
		RetryDelay: cloudDur(4 * time.Second), MaxRetryDelay: cloudDur(time.Minute),
	}}
	want := engine.Payload{
		"tenant_id": cloudTenantID, "resource": cloudAzureResource,
		"environment": cloudLitAzurePublicCloud, cloudLitClientID: cloudClientID,
		"client_secret": "fake-client-secret", cloudLitMaxRetries: 3, "retry_delay": 4, "max_retry_delay": 60,
	}
	read := map[string]any{
		"tenant_id": cloudTenantID, "resource": cloudAzureResource,
		"environment": cloudLitAzurePublicCloud, cloudLitClientID: cloudClientID,
		cloudLitMaxRetries: 3, "retry_delay": 4000000000, "max_retry_delay": 60000000000,
	}
	a := AzureAuthConfigAdapter{}
	cloudCheckGolden(t, a, obj, "auth/azure/config", want, read, "retry_delay", 10000000000)
	if got := a.SecretRefs(obj); !slices.Equal(got, []string{"azure-creds"}) {
		t.Errorf("secret refs = %v", got)
	}
}

func TestAzureAuthRoleGolden(t *testing.T) {
	obj := &authv1alpha1.AzureAuthRole{ObjectMeta: cloudMeta("vm"), Spec: authv1alpha1.AzureAuthRoleSpec{
		MountPath: "azure-prod", Name: "vm-role",
		BoundServicePrincipalIDs: []string{authLitSp1, authLitSp2}, BoundGroupIDs: []string{cloudLitGroup1},
		BoundLocations: []string{authLitWesteurope}, BoundSubscriptionIDs: []string{authLitSub1},
		BoundResourceGroups: []string{authLitRgApp}, BoundScaleSets: []string{authLitVmssApp}, TokenFields: cloudTokenFields,
	}}
	want := cloudWithToken(engine.Payload{
		"bound_service_principal_ids": []string{authLitSp1, authLitSp2}, "bound_group_ids": []string{cloudLitGroup1},
		"bound_locations": []string{authLitWesteurope}, "bound_subscription_ids": []string{authLitSub1},
		"bound_resource_groups": []string{authLitRgApp}, "bound_scale_sets": []string{authLitVmssApp},
	}, cloudTokenWant)
	read := cloudWithToken(map[string]any{
		"bound_service_principal_ids": []any{authLitSp2, authLitSp1}, "bound_group_ids": []any{cloudLitGroup1},
		"bound_locations": []any{authLitWesteurope}, "bound_subscription_ids": []any{authLitSub1},
		"bound_resource_groups": []any{authLitRgApp}, "bound_scale_sets": []any{authLitVmssApp},
	}, cloudTokenRead)
	cloudCheckGolden(t, AzureAuthRoleAdapter{}, obj, "auth/azure-prod/role/vm-role", want, read, "bound_group_ids", []any{"group-2"})
}

func TestGCPAuthConfigGolden(t *testing.T) {
	obj := &authv1alpha1.GCPAuthConfig{ObjectMeta: cloudMeta("gcp"), Spec: authv1alpha1.GCPAuthConfigSpec{
		CredentialsSecretRef: cloudRef("gcp-creds", "key.json"),
		IAMAlias:             authLitUniqueID, IAMMetadata: []string{cloudLitProjectID, authLitRole},
		GCEAlias: cloudLitInstanceID, GCEMetadata: []string{authLitZone, cloudLitInstanceID},
		CustomEndpoint: &authv1alpha1.GCPAuthCustomEndpoint{
			API: cloudLitWwwGoogleapisExample, IAM: cloudGCPIAMEndpoint,
			CRM: cloudLitCrmGoogleapisExample, Compute: cloudGCPComputeEndpoint,
		},
	}}
	want := engine.Payload{
		"credentials":    `{"type":"service_account","private_key":"fake"}`,
		cloudLitIAMAlias: authLitUniqueID, cloudLitIAMMetadata: []string{cloudLitProjectID, authLitRole},
		"gce_alias": cloudLitInstanceID, "gce_metadata": []string{authLitZone, cloudLitInstanceID},
		"custom_endpoint": map[string]any{
			cloudKeyAPI: cloudLitWwwGoogleapisExample, cloudLitIAM: cloudGCPIAMEndpoint,
			"crm": cloudLitCrmGoogleapisExample, "compute": cloudGCPComputeEndpoint,
		},
	}
	read := map[string]any{
		cloudLitIAMAlias: authLitUniqueID, cloudLitIAMMetadata: []any{authLitRole, cloudLitProjectID},
		"gce_alias": cloudLitInstanceID, "gce_metadata": []any{cloudLitInstanceID, authLitZone},
		"custom_endpoint": map[string]any{
			cloudKeyAPI: cloudLitWwwGoogleapisExample, cloudLitIAM: cloudGCPIAMEndpoint,
			"crm": cloudLitCrmGoogleapisExample, "compute": cloudGCPComputeEndpoint,
		},
		"client_email": "openbao@project.iam.gserviceaccount.com", cloudLitClientID: "1234", "private_key_id": "abcd",
		cloudLitProjectID: cloudLitProject,
	}
	cloudCheckGolden(t, GCPAuthConfigAdapter{}, obj, "auth/gcp/config", want, read, "custom_endpoint",
		map[string]any{cloudKeyAPI: "https://other.example"})
}

func TestGCPAuthRoleGolden(t *testing.T) {
	gce := &authv1alpha1.GCPAuthRole{ObjectMeta: cloudMeta(cloudLitGCE), Spec: authv1alpha1.GCPAuthRoleSpec{
		Type: cloudLitGCE, BoundServiceAccounts: []string{cloudSAEmail},
		BoundProjects: []string{cloudLitProject}, AddGroupAliases: new(true), BoundZones: []string{cloudLitEuropeWest3A},
		BoundRegions: []string{cloudLitEuropeWest3}, BoundInstanceGroups: []string{cloudInstanceGroup},
		BoundLabels: map[string]string{pluginTestNamespace: "a", cloudLitEnv: "prod"}, TokenFields: cloudTokenFields,
	}}
	want := cloudWithToken(engine.Payload{
		authLitType: cloudLitGCE, cloudLitBoundServiceAccounts: []string{cloudSAEmail},
		"bound_projects": []string{cloudLitProject}, "add_group_aliases": true, "bound_zones": []string{cloudLitEuropeWest3A},
		"bound_regions": []string{cloudLitEuropeWest3}, "bound_instance_groups": []string{cloudInstanceGroup},
		"bound_labels": []string{"env:prod", "team:a"},
	}, cloudTokenWant)
	read := cloudWithToken(map[string]any{
		authLitRoleType: cloudLitGCE, cloudLitBoundServiceAccounts: []any{cloudSAEmail},
		"bound_projects": []any{cloudLitProject}, "add_group_aliases": true, "bound_zones": []any{cloudLitEuropeWest3A},
		"bound_regions": []any{cloudLitEuropeWest3}, "bound_instance_groups": []any{cloudInstanceGroup},
		"bound_labels": map[string]any{pluginTestNamespace: "a", cloudLitEnv: "prod"}, cloudLitMaxJwtExp: 900, cloudKeyAllowGCEInference: false,
		authLitRoleID: "uuid",
	}, cloudTokenRead)
	cloudCheckGolden(t, GCPAuthRoleAdapter{}, gce, "auth/gcp/role/gce", want, read, "bound_labels", map[string]any{cloudLitEnv: "dev"})

	iam := &authv1alpha1.GCPAuthRole{ObjectMeta: cloudMeta(cloudLitIAM), Spec: authv1alpha1.GCPAuthRoleSpec{
		Type: cloudLitIAM, BoundServiceAccounts: []string{"*"}, MaxJWTExp: cloudDur(15 * time.Minute), AllowGCEInference: new(true),
	}}
	wantIAM := engine.Payload{authLitType: cloudLitIAM, cloudLitBoundServiceAccounts: []string{"*"}, cloudLitMaxJwtExp: 900, cloudKeyAllowGCEInference: true}
	readIAM := map[string]any{authLitType: cloudLitIAM, cloudLitBoundServiceAccounts: []any{"*"}, cloudLitMaxJwtExp: 900, cloudKeyAllowGCEInference: true}
	cloudCheckGolden(t, GCPAuthRoleAdapter{}, iam, "auth/gcp/role/iam", wantIAM, readIAM, cloudLitMaxJwtExp, 3600)
}

func TestOCIAuthGolden(t *testing.T) {
	cfg := &authv1alpha1.OCIAuthConfig{ObjectMeta: cloudMeta("oci"), Spec: authv1alpha1.OCIAuthConfigSpec{
		HomeTenancyID: cloudLitOcid1TenancyOc1Fake,
	}}
	cloudCheckGolden(t, OCIAuthConfigAdapter{}, cfg, "auth/oci/config", engine.Payload{"home_tenancy_id": cloudLitOcid1TenancyOc1Fake},
		map[string]any{"home_tenancy_id": cloudLitOcid1TenancyOc1Fake}, "home_tenancy_id", "ocid1.tenancy.oc1..other")

	role := &authv1alpha1.OCIAuthRole{ObjectMeta: cloudMeta("ops"), Spec: authv1alpha1.OCIAuthRoleSpec{
		OCIDList: []string{cloudLitOcid1GroupOc1A, cloudLitOcid1DynamicgroupOc1B}, TokenFields: cloudTokenFields,
	}}
	want := cloudWithToken(engine.Payload{"ocid_list": []string{cloudLitOcid1GroupOc1A, cloudLitOcid1DynamicgroupOc1B}}, cloudTokenWant)
	read := cloudWithToken(map[string]any{"ocid_list": []any{cloudLitOcid1DynamicgroupOc1B, cloudLitOcid1GroupOc1A}}, cloudTokenRead)
	cloudCheckGolden(t, OCIAuthRoleAdapter{}, role, "auth/oci/role/ops", want, read, keyTokenTTL, 60)
}

func TestCFAuthGolden(t *testing.T) {
	cfg := &authv1alpha1.CFAuthConfig{ObjectMeta: cloudMeta("cf"), Spec: authv1alpha1.CFAuthConfigSpec{
		IdentityCACertificates: []string{cloudFakeCert}, CFAPIAddr: cloudCFAPIAddr,
		CFUsername: cloudLitOpenbao, CFPasswordSecretRef: cloudRef("cf-creds", authLitPassword),
		CFAPITrustedCertificates: []string{cloudFakeCert}, CFAPIMutualTLSCertificate: cloudFakeCert,
		CFAPIMutualTLSKeySecretRef: cloudRef("cf-creds", "mtls-key"),
		LoginMaxSecondsNotBefore:   new(int32(300)), LoginMaxSecondsNotAfter: new(int32(60)),
	}}
	want := engine.Payload{
		"identity_ca_certificates": []string{cloudFakeCert}, "cf_api_addr": cloudCFAPIAddr, "cf_username": cloudLitOpenbao,
		"cf_password": "fake-password", "cf_api_trusted_certificates": []string{cloudFakeCert},
		"cf_api_mutual_tls_certificate": cloudFakeCert, "cf_api_mutual_tls_key": cloudSecrets["cf-creds/mtls-key"],
		"login_max_seconds_not_before": 300, "login_max_seconds_not_after": 60,
	}
	read := map[string]any{
		"identity_ca_certificates": []any{cloudFakeCert}, "cf_api_addr": cloudCFAPIAddr, "cf_username": cloudLitOpenbao,
		"cf_api_trusted_certificates": []any{cloudFakeCert}, "cf_api_mutual_tls_certificate": cloudFakeCert,
		"login_max_seconds_not_before": 300, "login_max_seconds_not_after": 60,
	}
	a := CFAuthConfigAdapter{}
	cloudCheckGolden(t, a, cfg, "auth/cf/config", want, read, "cf_api_addr", "https://other.example")
	if got := a.SecretRefs(cfg); !slices.Equal(got, []string{"cf-creds", "cf-creds"}) {
		t.Errorf("secret refs = %v", got)
	}

	role := &authv1alpha1.CFAuthRole{ObjectMeta: cloudMeta(authTestApp), Spec: authv1alpha1.CFAuthRoleSpec{
		BoundApplicationIDs: []string{cloudAppID}, BoundSpaceIDs: []string{authLitSpace1},
		BoundOrganizationIDs: []string{cloudLitOrg1}, BoundInstanceIDs: []string{cloudLitInst1, cloudLitInst2},
		DisableIPMatching: new(false), TokenFields: cloudTokenFields,
	}}
	wantRole := cloudWithToken(engine.Payload{
		"bound_application_ids": []string{cloudAppID}, "bound_space_ids": []string{authLitSpace1},
		"bound_organization_ids": []string{cloudLitOrg1}, "bound_instance_ids": []string{cloudLitInst1, cloudLitInst2},
		"disable_ip_matching": false,
	}, cloudTokenWant)
	readRole := cloudWithToken(map[string]any{
		"bound_application_ids": []any{cloudAppID}, "bound_space_ids": []any{authLitSpace1},
		"bound_organization_ids": []any{cloudLitOrg1}, "bound_instance_ids": []any{cloudLitInst2, cloudLitInst1},
		"disable_ip_matching": false,
	}, cloudTokenRead)
	cloudCheckGolden(t, CFAuthRoleAdapter{}, role, "auth/cf/roles/app", wantRole, readRole, "disable_ip_matching", true)
}

func TestExternalAuthMountCheck(t *testing.T) {
	ctx := context.Background()
	a := OCIAuthRoleAdapter{}
	obj := &authv1alpha1.OCIAuthRole{ObjectMeta: cloudMeta("ops"), Spec: authv1alpha1.OCIAuthRoleSpec{MountPath: "oci-prod"}}

	missing := &cloudClient{data: map[string]map[string]any{pluginSysAuth: cloudSysAuth}}
	_, found, err := a.Observe(ctx, missing, obj)
	if found || !errors.Is(err, ErrAuthMountMissing) || !errors.Is(err, engine.ErrUnsupported) ||
		!strings.Contains(err.Error(), "auth/oci-prod") {
		t.Fatalf("missing mount: found=%v err=%v", found, err)
	}

	enabled := &cloudClient{data: map[string]map[string]any{pluginSysAuth: {"oci-prod/": map[string]any{authLitType: "oci"}}}}
	if _, found, err := a.Observe(ctx, enabled, obj); found || err != nil {
		t.Fatalf("absent role on an enabled mount: found=%v err=%v", found, err)
	}

	denied := &cloudClient{readErr: map[string]error{pluginSysAuth: &openbao.ResponseError{StatusCode: 403}}}
	if _, found, err := a.Observe(ctx, denied, obj); found || err != nil {
		t.Fatalf("a token without sys/auth access skips the check: found=%v err=%v", found, err)
	}

	broken := &cloudClient{readErr: map[string]error{"auth/oci-prod/role/ops": &openbao.ResponseError{StatusCode: 500}}}
	if _, _, err := a.Observe(ctx, broken, obj); err == nil || errors.Is(err, openbao.ErrNotFound) {
		t.Fatalf("a 500 must be a real error: %v", err)
	}
}
