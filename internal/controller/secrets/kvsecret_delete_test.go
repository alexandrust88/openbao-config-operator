package secrets

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"reflect"
	"strconv"
	"testing"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// kvStore is one KV version 2 secret with check-and-set; interleave runs once, right before the first change.
type kvStore struct {
	openbao.Client
	versions   []map[string]any
	purged     bool
	interleave func(s *kvStore)
}

func (s *kvStore) latest() map[string]any { return s.versions[len(s.versions)-1] }

func (s *kvStore) Read(_ context.Context, _ string) (map[string]any, error) {
	if len(s.versions) == 0 {
		return nil, openbao.ErrNotFound
	}
	return map[string]any{"data": maps.Clone(s.latest()),
		"metadata": map[string]any{"version": json.Number(strconv.Itoa(len(s.versions)))}}, nil
}

func (s *kvStore) before() {
	if f := s.interleave; f != nil {
		s.interleave = nil
		f(s)
	}
}

func (s *kvStore) Write(_ context.Context, _ string, body map[string]any) (map[string]any, error) {
	s.before()
	cas, _ := body["options"].(map[string]any)["cas"].(int64)
	if cas != int64(len(s.versions)) {
		return nil, &openbao.ResponseError{StatusCode: http.StatusBadRequest,
			Errors: []string{"check-and-set parameter did not match the current version"}}
	}
	data, _ := body["data"].(map[string]any)
	s.versions = append(s.versions, maps.Clone(data))
	return nil, nil
}

func (s *kvStore) Delete(_ context.Context, _ string) error {
	s.before()
	s.versions, s.purged = nil, true
	return nil
}

// TestKVRemoveLastKeyKeepsConcurrentWrite: a key another writer adds between the read and the removal of the last
// owned key survives, and so does the history of the path.
func TestKVRemoveLastKeyKeepsConcurrentWrite(t *testing.T) {
	store := &kvStore{versions: []map[string]any{{"username": "u"}}}
	store.interleave = func(s *kvStore) {
		s.versions = append(s.versions, map[string]any{"username": "u", "other": "o"})
	}
	kv := newKVSecret(2)
	kv.Status.ManagedKeys = []string{"username"}
	if err := (KVSecretAdapter{}).Delete(context.Background(), store, kv); err == nil {
		t.Error("a removal that lost the race must fail and be retried")
	}
	if err := (KVSecretAdapter{}).Delete(context.Background(), store, kv); err != nil {
		t.Fatal(err)
	}
	if store.purged || len(store.versions) != 3 || !reflect.DeepEqual(store.latest(), map[string]any{"other": "o"}) {
		t.Errorf("purged=%v versions=%v, want the concurrent key kept", store.purged, store.versions)
	}
}

// TestKVRemoveLastKeyPurgesOnlyExclusivePaths: removing the last key leaves an empty version unless the resource
// created the path and carries the destructive-delete annotation.
func TestKVRemoveLastKeyPurgesOnlyExclusivePaths(t *testing.T) {
	for name, tt := range map[string]struct {
		acquired   commonv1alpha1.AcquiredState
		annotation string
		purge      bool
	}{
		"adopted":          {acquired: commonv1alpha1.AcquiredAdopted, annotation: "true"},
		"created":          {acquired: commonv1alpha1.AcquiredCreated},
		"created, allowed": {acquired: commonv1alpha1.AcquiredCreated, annotation: "true", purge: true},
	} {
		t.Run(name, func(t *testing.T) {
			store := &kvStore{versions: []map[string]any{{"username": "u"}}}
			kv := newKVSecret(2)
			kv.Status.ManagedKeys, kv.Status.Acquired = []string{"username"}, tt.acquired
			kv.Annotations = map[string]string{commonv1alpha1.AnnotationAllowDestructiveDelete: tt.annotation}
			if err := (KVSecretAdapter{}).Delete(context.Background(), store, kv); err != nil {
				t.Fatal(err)
			}
			if store.purged != tt.purge {
				t.Errorf("purged = %v, want %v", store.purged, tt.purge)
			}
			if !tt.purge && (len(store.versions) != 2 || len(store.latest()) != 0) {
				t.Errorf("versions = %v, want the history plus an empty version", store.versions)
			}
		})
	}
}
