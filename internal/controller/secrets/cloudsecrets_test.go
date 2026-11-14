package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	secretsv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/secrets/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

type cloudFakeResolver map[string]string

func (r cloudFakeResolver) SecretValue(_ context.Context, namespace string, ref commonv1alpha1.SecretKeySelector) ([]byte, error) {
	if v, ok := r[ref.Name+"/"+ref.Key]; ok {
		return []byte(v), nil
	}
	return nil, fmt.Errorf("secret %s/%s: %w", namespace, ref.Name, engine.ErrReferenceNotReady)
}

func (cloudFakeResolver) Object(context.Context, client.ObjectKey, client.Object) error {
	return errors.New("not used")
}

type cloudWrite struct {
	path string
	data map[string]any
}

type cloudFakeClient struct {
	openbao.Client
	reads    map[string]map[string]any
	readErr  error
	writeErr error
	writes   []cloudWrite
}

// Read returns a JSON round trip with json.Number, as the real client does.
func (c *cloudFakeClient) Read(_ context.Context, path string) (map[string]any, error) {
	if c.readErr != nil {
		return nil, c.readErr
	}
	data, ok := c.reads[path]
	if !ok {
		return nil, openbao.ErrNotFound
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return decodeCloudJSON(encoded)
}

func (c *cloudFakeClient) Write(_ context.Context, path string, data map[string]any) (map[string]any, error) {
	c.writes = append(c.writes, cloudWrite{path: path, data: data})
	return nil, c.writeErr
}

func decodeCloudJSON(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out map[string]any
	return out, dec.Decode(&out)
}

func readCloudGolden(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cloud", name))
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeCloudJSON(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func cloudDur(d time.Duration) *metav1.Duration { return &metav1.Duration{Duration: d} }

func cloudRef(name, key string) *commonv1alpha1.SecretKeySelector {
	return &commonv1alpha1.SecretKeySelector{Name: name, Key: key}
}

const (
	cloudAzureSecret = "azure-sp"
	cloudReaderRole  = "Reader"
)

var cloudSecrets = cloudFakeResolver{
	"aws-root/access_key":        "AKIAFAKEEXAMPLE00001",
	"aws-root/secret_key":        "fake-aws-secret-key",
	"azure-sp/client_id":         "11111111-1111-1111-1111-111111111111",
	"azure-sp/client_secret":     "fake-azure-client-secret",
	"consul-mgmt/token":          "fake-consul-token",
	"consul-mgmt/client_key":     "fake-client-key",
	"gcp-sa/credentials.json":    `{"type":"service_account","project_id":"obco-fake"}`,
	"gcp-sa-rotated/credentials": `{"type":"service_account","project_id":"obco-rotated"}`,
}

func cloudMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: "openbao-config"}
}

var cloudConn = commonv1alpha1.ResourceSpec{ConnectionRef: commonv1alpha1.ConnectionReference{Name: "openbao"}}

func fullAWSSecretsConfig() *secretsv1alpha1.AWSSecretsConfig {
	return &secretsv1alpha1.AWSSecretsConfig{
		ObjectMeta: cloudMeta("aws"),
		Spec: secretsv1alpha1.AWSSecretsConfigSpec{
			ResourceSpec:       cloudConn,
			MountPath:          "aws-prod",
			AccessKeySecretRef: cloudRef("aws-root", "access_key"),
			SecretKeySecretRef: cloudRef("aws-root", "secret_key"),
			Region:             "eu-central-1",
			IAMEndpoint:        "https://iam.amazonaws.com",
			STSEndpoint:        "https://sts.eu-central-1.amazonaws.com",
			MaxRetries:         new(int32(3)),
			UsernameTemplate:   `{{ printf "obco-%s" (random 20) }}`,
			Lease:              cloudDur(30 * time.Minute),
			LeaseMax:           cloudDur(12 * time.Hour),
		},
	}
}

func fullAWSSecretsRole() *secretsv1alpha1.AWSSecretsRole {
	return &secretsv1alpha1.AWSSecretsRole{
		ObjectMeta: cloudMeta("aws-deploy"),
		Spec: secretsv1alpha1.AWSSecretsRoleSpec{
			ResourceSpec:   cloudConn,
			MountPath:      "aws-prod",
			Name:           "deploy",
			CredentialType: "assumed_role",
			RoleARNs:       []string{"arn:aws:iam::123456789012:role/obco-b", "arn:aws:iam::123456789012:role/obco-a"},
			PolicyARNs:     []string{"arn:aws:iam::aws:policy/ReadOnlyAccess"},
			PolicyDocument: "{\n  \"Version\": \"2012-10-17\",\n  \"Statement\": [\n    {\"Effect\": \"Allow\", \"Action\": \"s3:ListBucket\", \"Resource\": \"*\"}\n  ]\n}\n",
			IAMGroups:      []string{"obco-readers"},
			IAMTags:        map[string]string{"team": "platform"},
			SessionTags:    map[string]string{"env": "sandbox"},
			ExternalID:     "obco-external-id",
			DefaultSTSTTL:  cloudDur(15 * time.Minute),
			MaxSTSTTL:      cloudDur(time.Hour),
			UserPath:       "/obco/",

			PermissionsBoundaryARN: "arn:aws:iam::123456789012:policy/obco-boundary",
			MFASerialNumber:        "arn:aws:iam::123456789012:mfa/obco-device",
		},
	}
}

func fullAzureSecretsConfig() *secretsv1alpha1.AzureSecretsConfig {
	return &secretsv1alpha1.AzureSecretsConfig{
		ObjectMeta: cloudMeta("azure"),
		Spec: secretsv1alpha1.AzureSecretsConfigSpec{
			ResourceSpec:          cloudConn,
			MountPath:             "azure-prod",
			SubscriptionID:        "00000000-0000-0000-0000-000000000001",
			TenantID:              "00000000-0000-0000-0000-000000000002",
			ClientIDSecretRef:     cloudRef(cloudAzureSecret, "client_id"),
			ClientSecretSecretRef: cloudRef(cloudAzureSecret, "client_secret"),
			Environment:           "AzurePublicCloud",
			PasswordPolicy:        cloudAzureSecret,
			RootPasswordTTL:       cloudDur(4380 * time.Hour),
		},
	}
}

func fullAzureSecretsRole() *secretsv1alpha1.AzureSecretsRole {
	return &secretsv1alpha1.AzureSecretsRole{
		ObjectMeta: cloudMeta("app-reader"),
		Spec: secretsv1alpha1.AzureSecretsRoleSpec{
			ResourceSpec: cloudConn,
			MountPath:    "azure-prod",
			AzureRoles: []secretsv1alpha1.AzureRoleAssignment{
				{RoleName: cloudReaderRole, Scope: "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/obco"},
				{
					RoleID: "/subscriptions/00000000-0000-0000-0000-000000000001/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c",
					Scope:  "/subscriptions/00000000-0000-0000-0000-000000000001",
				},
			},
			AzureGroups:         []secretsv1alpha1.AzureGroupAssignment{{GroupName: "obco-readers"}},
			ApplicationObjectID: "33333333-3333-3333-3333-333333333333",
			PersistApp:          new(true),
			TTL:                 cloudDur(time.Hour),
			MaxTTL:              cloudDur(24 * time.Hour),
			ExplicitMaxTTL:      cloudDur(720 * time.Hour),
			PermanentlyDelete:   new(true),
			SignInAudience:      "AzureADMyOrg",
			Tags:                []string{"team:platform", "env:sandbox"},
		},
	}
}

func fullConsulSecretsConfig() *secretsv1alpha1.ConsulSecretsConfig {
	return &secretsv1alpha1.ConsulSecretsConfig{
		ObjectMeta: cloudMeta("consul"),
		Spec: secretsv1alpha1.ConsulSecretsConfigSpec{
			ResourceSpec:       cloudConn,
			MountPath:          "consul-prod",
			Address:            "consul.example.internal:8501",
			Scheme:             "https",
			TokenSecretRef:     *cloudRef("consul-mgmt", "token"),
			CACert:             "-----BEGIN CERTIFICATE-----\nFAKE-CA\n-----END CERTIFICATE-----\n",
			ClientCert:         "-----BEGIN CERTIFICATE-----\nFAKE-CLIENT\n-----END CERTIFICATE-----\n",
			ClientKeySecretRef: cloudRef("consul-mgmt", "client_key"),
		},
	}
}

func fullConsulSecretsRole() *secretsv1alpha1.ConsulSecretsRole {
	return &secretsv1alpha1.ConsulSecretsRole{
		ObjectMeta: cloudMeta("app"),
		Spec: secretsv1alpha1.ConsulSecretsRoleSpec{
			ResourceSpec:      cloudConn,
			MountPath:         "consul-prod",
			ConsulPolicies:    []string{"read-kv", "write-kv"},
			ConsulRoles:       []string{"ops"},
			ServiceIdentities: []string{"web:dc1,dc2"},
			NodeIdentities:    []string{"node-1:dc1"},
			ConsulNamespace:   "team-a",
			Partition:         "default",
			Local:             new(true),
			TTL:               cloudDur(time.Hour),
			MaxTTL:            cloudDur(24 * time.Hour),
		},
	}
}

func fullGCPSecretsConfig() *secretsv1alpha1.GCPSecretsConfig {
	return &secretsv1alpha1.GCPSecretsConfig{
		ObjectMeta: cloudMeta("gcp"),
		Spec: secretsv1alpha1.GCPSecretsConfigSpec{
			ResourceSpec:         cloudConn,
			MountPath:            "gcp-prod",
			CredentialsSecretRef: cloudRef("gcp-sa", "credentials.json"),
			TTL:                  cloudDur(time.Hour),
			MaxTTL:               cloudDur(24 * time.Hour),
		},
	}
}

const cloudProjectResource = "//cloudresourcemanager.googleapis.com/projects/example-project"

func fullGCPRoleset() *secretsv1alpha1.GCPRoleset {
	return &secretsv1alpha1.GCPRoleset{
		ObjectMeta: cloudMeta("viewer"),
		Spec: secretsv1alpha1.GCPRolesetSpec{
			ResourceSpec: cloudConn,
			MountPath:    "gcp-prod",
			SecretType:   "access_token",
			Project:      "example-project",
			Bindings: []secretsv1alpha1.GCPBinding{
				{Resource: cloudProjectResource, Roles: []string{"roles/viewer", "roles/browser"}},
				{Resource: "projects/example-project/topics/events", Roles: []string{"roles/pubsub.publisher"}},
			},
			TokenScopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
		},
	}
}

func fullGCPStaticAccount() *secretsv1alpha1.GCPStaticAccount {
	return &secretsv1alpha1.GCPStaticAccount{
		ObjectMeta: cloudMeta("ci-deployer"),
		Spec: secretsv1alpha1.GCPStaticAccountSpec{
			ResourceSpec:        cloudConn,
			MountPath:           "gcp-prod",
			SecretType:          "access_token",
			ServiceAccountEmail: "ci-deployer@example-project.iam.gserviceaccount.com",
			Bindings: []secretsv1alpha1.GCPBinding{
				{Resource: cloudProjectResource, Roles: []string{"roles/storage.objectViewer"}},
			},
			TokenScopes: []string{
				"https://www.googleapis.com/auth/cloud-platform",
				"https://www.googleapis.com/auth/devstorage.read_only",
			},
		},
	}
}

func fullGCPImpersonatedAccount() *secretsv1alpha1.GCPImpersonatedAccount {
	return &secretsv1alpha1.GCPImpersonatedAccount{
		ObjectMeta: cloudMeta("ci-runner"),
		Spec: secretsv1alpha1.GCPImpersonatedAccountSpec{
			ResourceSpec:        cloudConn,
			MountPath:           "gcp-prod",
			ServiceAccountEmail: "ci-runner@example-project.iam.gserviceaccount.com",
			TokenScopes:         []string{"https://www.googleapis.com/auth/cloud-platform"},
			TTL:                 cloudDur(30 * time.Minute),
		},
	}
}

// cloudCase drives one kind through its golden payload and golden read.
type cloudCase struct {
	golden   string
	path     string
	adapter  func() (path string, fields engine.FieldSpec)
	desired  func(r engine.Resolver) (engine.Payload, error)
	observe  func(c openbao.Client) (engine.Payload, bool, error)
	write    func(c openbao.Client, desired engine.Payload) error
	mutate   string
	mutation any
}

func newCloudCase[T engine.Object](golden string, a engine.Adapter[T], obj T, path string, mutate string, mutation any) cloudCase {
	return cloudCase{
		golden: golden, path: path, mutate: mutate, mutation: mutation,
		adapter: func() (string, engine.FieldSpec) {
			return a.Path(obj), a.Fields()
		},
		desired: func(r engine.Resolver) (engine.Payload, error) { return a.Desired(context.Background(), r, obj) },
		observe: func(c openbao.Client) (engine.Payload, bool, error) {
			if o, ok := any(a).(engine.Observer[T]); ok {
				return o.Observe(context.Background(), c, obj)
			}
			return readCloud(context.Background(), c, a.Path(obj))
		},
		write: func(c openbao.Client, desired engine.Payload) error {
			_, err := any(a).(engine.Writer[T]).Write(context.Background(), c, obj, desired, true)
			return err
		},
	}
}

func cloudCases() []cloudCase {
	return []cloudCase{
		newCloudCase("awssecretsconfig", AWSSecretsConfigAdapter{}, fullAWSSecretsConfig(),
			"aws-prod/config/root", "region", "us-east-1"),
		newCloudCase("awssecretsrole", AWSSecretsRoleAdapter{}, fullAWSSecretsRole(),
			"aws-prod/roles/deploy", "policy_arns", []any{"arn:aws:iam::aws:policy/AdministratorAccess"}),
		newCloudCase("azuresecretsconfig", AzureSecretsConfigAdapter{}, fullAzureSecretsConfig(),
			"azure-prod/config", "tenant_id", "00000000-0000-0000-0000-000000000009"),
		newCloudCase("azuresecretsrole", AzureSecretsRoleAdapter{}, fullAzureSecretsRole(),
			"azure-prod/roles/app-reader", "azure_roles", []any{map[string]any{"role_name": "Owner", "scope": "/"}}),
		newCloudCase("consulsecretsconfig", ConsulSecretsConfigAdapter{}, fullConsulSecretsConfig(),
			"consul-prod/config/access", "scheme", "http"),
		newCloudCase("consulsecretsrole", ConsulSecretsRoleAdapter{}, fullConsulSecretsRole(),
			"consul-prod/roles/app", "consul_policies", []any{"read-kv"}),
		newCloudCase("gcpsecretsconfig", GCPSecretsConfigAdapter{}, fullGCPSecretsConfig(),
			"gcp-prod/config", "ttl", 60),
		newCloudCase("gcproleset", GCPRolesetAdapter{}, fullGCPRoleset(),
			"gcp-prod/roleset/viewer", "bindings", map[string]any{cloudProjectResource: []any{"roles/viewer"}}),
		newCloudCase("gcpstaticaccount", GCPStaticAccountAdapter{}, fullGCPStaticAccount(),
			"gcp-prod/static-account/ci-deployer", "token_scopes", []any{"https://www.googleapis.com/auth/cloud-platform"}),
		newCloudCase("gcpimpersonatedaccount", GCPImpersonatedAccountAdapter{}, fullGCPImpersonatedAccount(),
			"gcp-prod/impersonated-account/ci-runner", "ttl", 3600),
	}
}

func TestCloudSecretsPath(t *testing.T) {
	for _, tc := range cloudCases() {
		if path, _ := tc.adapter(); path != tc.path {
			t.Errorf("%s: path = %q, want %q", tc.golden, path, tc.path)
		}
	}
}

func TestCloudSecretsGoldenPayload(t *testing.T) {
	for _, tc := range cloudCases() {
		t.Run(tc.golden, func(t *testing.T) {
			desired, err := tc.desired(cloudSecrets)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(desired)
			if err != nil {
				t.Fatal(err)
			}
			got, err := decodeCloudJSON(encoded)
			if err != nil {
				t.Fatal(err)
			}
			want := readCloudGolden(t, tc.golden+".payload.json")
			if !reflect.DeepEqual(got, want) {
				t.Errorf("payload differs from testdata/cloud/%s.payload.json\n got: %s", tc.golden, encoded)
			}
		})
	}
}

func TestCloudSecretsGoldenRead(t *testing.T) {
	for _, tc := range cloudCases() {
		t.Run(tc.golden, func(t *testing.T) {
			_, fields := tc.adapter()
			desired, err := tc.desired(cloudSecrets)
			if err != nil {
				t.Fatal(err)
			}
			reads := readCloudGolden(t, tc.golden+".read.json")
			fixture := map[string]map[string]any{}
			for path, data := range reads {
				fixture[path] = data.(map[string]any)
			}

			observed, found, err := tc.observe(&cloudFakeClient{reads: fixture})
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			if differing := engine.Diff(desired, observed, fields); len(differing) != 0 {
				t.Errorf("steady state differs on %v", differing)
			}

			fixture[tc.path][tc.mutate] = tc.mutation
			observed, _, err = tc.observe(&cloudFakeClient{reads: fixture})
			if err != nil {
				t.Fatal(err)
			}
			if differing := engine.Diff(desired, observed, fields); !reflect.DeepEqual(differing, []string{tc.mutate}) {
				t.Errorf("after changing %s, differing = %v", tc.mutate, differing)
			}

			if _, found, err := tc.observe(&cloudFakeClient{}); found || err != nil {
				t.Errorf("absent: found=%v err=%v", found, err)
			}
			boom := errors.New("permission denied")
			if _, _, err := tc.observe(&cloudFakeClient{readErr: boom}); !errors.Is(err, boom) {
				t.Errorf("read error not returned: %v", err)
			}
		})
	}
}

func TestCloudSecretsDefaults(t *testing.T) {
	tests := []struct {
		name, got, want string
	}{
		{"aws config", AWSSecretsConfigAdapter{}.Path(&secretsv1alpha1.AWSSecretsConfig{}), "aws/config/root"},
		{"aws role", AWSSecretsRoleAdapter{}.Path(&secretsv1alpha1.AWSSecretsRole{ObjectMeta: cloudMeta("r")}), "aws/roles/r"},
		{"azure config", AzureSecretsConfigAdapter{}.Path(&secretsv1alpha1.AzureSecretsConfig{}), "azure/config"},
		{"azure role", AzureSecretsRoleAdapter{}.Path(&secretsv1alpha1.AzureSecretsRole{ObjectMeta: cloudMeta("r")}), "azure/roles/r"},
		{"consul config", ConsulSecretsConfigAdapter{}.Path(&secretsv1alpha1.ConsulSecretsConfig{}), "consul/config/access"},
		{"consul role", ConsulSecretsRoleAdapter{}.Path(&secretsv1alpha1.ConsulSecretsRole{ObjectMeta: cloudMeta("r")}), "consul/roles/r"},
		{"gcp config", GCPSecretsConfigAdapter{}.Path(&secretsv1alpha1.GCPSecretsConfig{}), "gcp/config"},
		{"gcp roleset", GCPRolesetAdapter{}.Path(&secretsv1alpha1.GCPRoleset{ObjectMeta: cloudMeta("r")}), "gcp/roleset/r"},
		{"gcp static", GCPStaticAccountAdapter{}.Path(&secretsv1alpha1.GCPStaticAccount{ObjectMeta: cloudMeta("r")}), "gcp/static-account/r"},
		{"gcp impersonated", GCPImpersonatedAccountAdapter{}.Path(&secretsv1alpha1.GCPImpersonatedAccount{ObjectMeta: cloudMeta("r")}), "gcp/impersonated-account/r"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: path = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestCloudSecretsUnsetFieldsOmitted(t *testing.T) {
	ctx := context.Background()
	aws, err := AWSSecretsConfigAdapter{}.Desired(ctx, cloudSecrets, &secretsv1alpha1.AWSSecretsConfig{})
	if err != nil || len(aws) != 0 {
		t.Errorf("empty aws config: %v %v", aws, err)
	}
	role, err := AWSSecretsRoleAdapter{}.Desired(ctx, nil, &secretsv1alpha1.AWSSecretsRole{
		Spec: secretsv1alpha1.AWSSecretsRoleSpec{CredentialType: "iam_user"},
	})
	if err != nil || !reflect.DeepEqual(role, engine.Payload{"credential_type": "iam_user"}) {
		t.Errorf("minimal aws role: %v %v", role, err)
	}
	consul, err := ConsulSecretsConfigAdapter{}.Desired(ctx, cloudSecrets, &secretsv1alpha1.ConsulSecretsConfig{
		Spec: secretsv1alpha1.ConsulSecretsConfigSpec{Address: "c:8500", TokenSecretRef: *cloudRef("consul-mgmt", "token")},
	})
	if err != nil || len(consul) != 2 {
		t.Errorf("minimal consul config: %v", err)
	}
	static, err := GCPStaticAccountAdapter{}.Desired(ctx, nil, &secretsv1alpha1.GCPStaticAccount{
		Spec: secretsv1alpha1.GCPStaticAccountSpec{ServiceAccountEmail: "sa@p.iam.gserviceaccount.com"},
	})
	if err != nil || !reflect.DeepEqual(static, engine.Payload{"service_account_email": "sa@p.iam.gserviceaccount.com"}) {
		t.Errorf("minimal static account: %v %v", static, err)
	}
}

func TestCloudSecretsMissingSecret(t *testing.T) {
	ctx := context.Background()
	empty := cloudFakeResolver{}
	checks := map[string]func() error{
		"aws": func() error {
			_, err := AWSSecretsConfigAdapter{}.Desired(ctx, empty, fullAWSSecretsConfig())
			return err
		},
		"azure": func() error {
			_, err := AzureSecretsConfigAdapter{}.Desired(ctx, empty, fullAzureSecretsConfig())
			return err
		},
		"consul": func() error {
			_, err := ConsulSecretsConfigAdapter{}.Desired(ctx, empty, fullConsulSecretsConfig())
			return err
		},
		"gcp": func() error {
			_, err := GCPSecretsConfigAdapter{}.Desired(ctx, empty, fullGCPSecretsConfig())
			return err
		},
	}
	for name, check := range checks {
		if err := check(); !errors.Is(err, engine.ErrReferenceNotReady) {
			t.Errorf("%s: err = %v, want ErrReferenceNotReady", name, err)
		}
	}
}

func TestCloudSecretsSecretRefs(t *testing.T) {
	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{"aws", AWSSecretsConfigAdapter{}.SecretRefs(fullAWSSecretsConfig()), []string{"aws-root", "aws-root"}},
		{"aws none", AWSSecretsConfigAdapter{}.SecretRefs(&secretsv1alpha1.AWSSecretsConfig{}), nil},
		{"azure", AzureSecretsConfigAdapter{}.SecretRefs(fullAzureSecretsConfig()), []string{cloudAzureSecret, cloudAzureSecret}},
		{"consul", ConsulSecretsConfigAdapter{}.SecretRefs(fullConsulSecretsConfig()), []string{"consul-mgmt", "consul-mgmt"}},
		{"gcp", GCPSecretsConfigAdapter{}.SecretRefs(fullGCPSecretsConfig()), []string{"gcp-sa"}},
	}
	for _, tt := range tests {
		if !reflect.DeepEqual(tt.got, tt.want) {
			t.Errorf("%s: refs = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

var cloudHashKey = []byte("cloud-test-hash-key")

func TestCloudSecretsWriteOnlyHash(t *testing.T) {
	ctx := context.Background()
	a := GCPSecretsConfigAdapter{}
	cfg := fullGCPSecretsConfig()
	before, err := a.Desired(ctx, cloudSecrets, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Spec.CredentialsSecretRef = cloudRef("gcp-sa-rotated", "credentials")
	after, err := a.Desired(ctx, cloudSecrets, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if engine.WriteOnlyHash(cloudHashKey, before, a.Fields()) == engine.WriteOnlyHash(cloudHashKey, after, a.Fields()) {
		t.Error("rotated credentials did not change the write-only hash")
	}

	c := ConsulSecretsConfigAdapter{}
	consul := fullConsulSecretsConfig()
	desired, _ := c.Desired(ctx, cloudSecrets, consul)
	consul.Spec.CACert = "-----BEGIN CERTIFICATE-----\nOTHER\n-----END CERTIFICATE-----\n"
	changed, _ := c.Desired(ctx, cloudSecrets, consul)
	if engine.WriteOnlyHash(cloudHashKey, desired, c.Fields()) == engine.WriteOnlyHash(cloudHashKey, changed, c.Fields()) {
		t.Error("a changed caCert did not change the write-only hash")
	}
}

func TestAWSSecretsConfigWriteSplitsPaths(t *testing.T) {
	ctx := context.Background()
	a := AWSSecretsConfigAdapter{}
	cfg := fullAWSSecretsConfig()
	desired, err := a.Desired(ctx, cloudSecrets, cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := &cloudFakeClient{}
	if _, err := a.Write(ctx, c, cfg, desired, true); err != nil {
		t.Fatal(err)
	}
	if len(c.writes) != 2 || c.writes[0].path != "aws-prod/config/root" || c.writes[1].path != "aws-prod/config/lease" {
		t.Fatalf("writes = %v", c.writes)
	}
	if _, ok := c.writes[0].data["lease"]; ok {
		t.Error("lease written to config/root")
	}
	if c.writes[0].data["secret_key"] != "fake-aws-secret-key" || c.writes[0].data["region"] != "eu-central-1" {
		t.Error("config/root misses keys")
	}
	if !reflect.DeepEqual(c.writes[1].data, map[string]any{"lease": "30m0s", "lease_max": "12h0m0s"}) {
		t.Errorf("config/lease = %v", c.writes[1].data)
	}
	if _, ok := desired["lease"]; !ok {
		t.Error("Write mutated the desired payload")
	}

	cfg.Spec.Lease, cfg.Spec.LeaseMax = nil, nil
	desired, _ = a.Desired(ctx, cloudSecrets, cfg)
	c = &cloudFakeClient{}
	if _, err := a.Write(ctx, c, cfg, desired, false); err != nil || len(c.writes) != 1 {
		t.Errorf("without lease: writes = %v, err = %v", c.writes, err)
	}
}

func TestAWSSecretsConfigObserveWithoutLease(t *testing.T) {
	c := &cloudFakeClient{reads: map[string]map[string]any{"aws-prod/config/root": {"region": "eu-central-1"}}}
	observed, found, err := AWSSecretsConfigAdapter{}.Observe(context.Background(), c, fullAWSSecretsConfig())
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if _, ok := observed["lease"]; ok {
		t.Error("lease reported although config/lease is absent")
	}
}

func TestAWSSecretsRoleInvalidPolicyDocument(t *testing.T) {
	r := fullAWSSecretsRole()
	r.Spec.PolicyDocument = `{"Version": "2012-10-17", "secret": "do-not-echo"`
	_, err := AWSSecretsRoleAdapter{}.Desired(context.Background(), nil, r)
	if err == nil || strings.Contains(err.Error(), "do-not-echo") {
		t.Errorf("err = %v", err)
	}
}

func TestAzureSecretsRoleObserveMatching(t *testing.T) {
	scope := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/obco"
	role := &secretsv1alpha1.AzureSecretsRole{
		ObjectMeta: cloudMeta("r"),
		Spec: secretsv1alpha1.AzureSecretsRoleSpec{
			AzureRoles: []secretsv1alpha1.AzureRoleAssignment{{RoleName: cloudReaderRole, Scope: scope}},
		},
	}
	tests := []struct {
		name   string
		stored []any
		inSync bool
	}{
		{"id filled in, case differs", []any{map[string]any{"role_name": "reader", "role_id": "/x", "scope": strings.ToUpper(scope)}}, true},
		{"other scope", []any{map[string]any{"role_name": cloudReaderRole, "scope": "/subscriptions/other"}}, false},
		{"extra assignment", []any{
			map[string]any{"role_name": cloudReaderRole, "scope": scope},
			map[string]any{"role_name": "Owner", "scope": scope},
		}, false},
	}
	a := AzureSecretsRoleAdapter{}
	desired, _ := a.Desired(context.Background(), nil, role)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &cloudFakeClient{reads: map[string]map[string]any{"azure/roles/r": {"azure_roles": tt.stored}}}
			observed, _, err := a.Observe(context.Background(), c, role)
			if err != nil {
				t.Fatal(err)
			}
			if inSync := len(engine.Diff(desired, observed, a.Fields())) == 0; inSync != tt.inSync {
				t.Errorf("in sync = %v, want %v", inSync, tt.inSync)
			}
		})
	}
}

func TestAzureSecretsRoleEmptyRoles(t *testing.T) {
	role := &secretsv1alpha1.AzureSecretsRole{
		ObjectMeta: cloudMeta("r"),
		Spec: secretsv1alpha1.AzureSecretsRoleSpec{
			AzureRoles:  []secretsv1alpha1.AzureRoleAssignment{},
			AzureGroups: []secretsv1alpha1.AzureGroupAssignment{{GroupName: "obco-readers"}},
		},
	}
	a := AzureSecretsRoleAdapter{}
	desired, _ := a.Desired(context.Background(), nil, role)
	if desired["azure_roles"] != "[]" {
		t.Fatalf("azure_roles = %v, want []", desired["azure_roles"])
	}
	for _, stored := range []any{nil, []any{}} {
		c := &cloudFakeClient{reads: map[string]map[string]any{"azure/roles/r": {
			"azure_roles":  stored,
			"azure_groups": []any{map[string]any{"group_name": "obco-readers", "object_id": "g"}},
		}}}
		observed, _, err := a.Observe(context.Background(), c, role)
		if err != nil {
			t.Fatal(err)
		}
		if differing := engine.Diff(desired, observed, a.Fields()); len(differing) != 0 {
			t.Errorf("stored %v: differing = %v", stored, differing)
		}
	}
}

// TestCloudSecretsWriteUnsupported proves that a missing plugin or mount stalls the resource as Unsupported, while
// a validation error stays a real error.
func TestCloudSecretsWriteUnsupported(t *testing.T) {
	missing := []error{
		&openbao.ResponseError{StatusCode: 404},
		&openbao.ResponseError{StatusCode: 405, Errors: []string{"no handler for route \"aws/config/root\". route entry not found."}},
	}
	invalid := &openbao.ResponseError{StatusCode: 400, Errors: []string{"invalid value"}}
	for _, tc := range cloudCases() {
		t.Run(tc.golden, func(t *testing.T) {
			desired, err := tc.desired(cloudSecrets)
			if err != nil {
				t.Fatal(err)
			}
			c := &cloudFakeClient{}
			if err := tc.write(c, desired); err != nil || len(c.writes) == 0 || c.writes[0].path != tc.path {
				t.Fatalf("writes = %v, err = %v", c.writes, err)
			}
			for _, cause := range missing {
				if err := tc.write(&cloudFakeClient{writeErr: cause}, desired); !errors.Is(err, engine.ErrUnsupported) {
					t.Errorf("%v: err = %v, want ErrUnsupported", cause, err)
				}
			}
			err = tc.write(&cloudFakeClient{writeErr: invalid}, desired)
			if err == nil || errors.Is(err, engine.ErrUnsupported) {
				t.Errorf("400: err = %v, want a plain error", err)
			}
		})
	}
}

func TestGCPRolesetWriteStatus(t *testing.T) {
	r := fullGCPRoleset()
	err := GCPRolesetAdapter{}.WriteStatus(context.Background(), nil, r,
		engine.Payload{"service_account_email": "vaultviewer-1@example-project.iam.gserviceaccount.com"})
	if err != nil || r.Status.ID != "vaultviewer-1@example-project.iam.gserviceaccount.com" {
		t.Errorf("status.id = %q, err = %v", r.Status.ID, err)
	}
}

func TestGCPBindingsMatch(t *testing.T) {
	bindings := []secretsv1alpha1.GCPBinding{{Resource: "projects/p", Roles: []string{"roles/a", "roles/b"}}}
	tests := []struct {
		name     string
		observed any
		want     bool
	}{
		{"reordered", map[string]any{"projects/p": []any{"roles/b", "roles/a"}}, true},
		{"missing role", map[string]any{"projects/p": []any{"roles/a"}}, false},
		{"extra resource", map[string]any{"projects/p": []any{"roles/a", "roles/b"}, "projects/q": []any{"roles/a"}}, false},
		{"other resource", map[string]any{"projects/q": []any{"roles/a", "roles/b"}}, false},
		{"absent", nil, false},
	}
	for _, tt := range tests {
		if got := gcpBindingsMatch(bindings, tt.observed); got != tt.want {
			t.Errorf("%s: match = %v, want %v", tt.name, got, tt.want)
		}
	}
}
