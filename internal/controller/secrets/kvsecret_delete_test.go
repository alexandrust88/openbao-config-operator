package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	commonv1alpha1 "github.com/alexandrust88/openbao-config-operator/api/common/v1alpha1"
	"github.com/alexandrust88/openbao-config-operator/internal/engine"
	"github.com/alexandrust88/openbao-config-operator/internal/openbao"
)

// kvStore is one KV version 2 secret with check-and-set; interleave runs once, right before the first change.
type kvStore struct {
	openbao.Client
	versions      []map[string]any
	purged        bool
	interleave    func(s *kvStore)
	beforeDestroy func(s *kvStore) error
	destroyed     map[int64]bool
}

func (s *kvStore) latest() map[string]any { return s.versions[len(s.versions)-1] }

func (s *kvStore) Read(_ context.Context, path string) (map[string]any, error) {
	if len(s.versions) == 0 {
		return nil, openbao.ErrNotFound
	}
	if strings.Contains(path, "/metadata/") {
		versions := map[string]any{}
		for i := range s.versions {
			versions[strconv.Itoa(i+1)] = map[string]any{"destroyed": s.destroyed[int64(i+1)]}
		}
		return map[string]any{"versions": versions}, nil
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

func (s *kvStore) Write(_ context.Context, path string, body map[string]any) (map[string]any, error) {
	if strings.Contains(path, "/destroy/") {
		if s.beforeDestroy != nil {
			if err := s.beforeDestroy(s); err != nil {
				return nil, err
			}
		}
		if s.destroyed == nil {
			s.destroyed = map[int64]bool{}
		}
		for _, version := range body["versions"].([]int64) {
			if version <= int64(len(s.versions)) {
				s.versions[version-1] = nil
				s.destroyed[version] = true
			}
		}
		s.purged = true
		return nil, nil
	}
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
	if err := finishKVDelete(func() error { return (KVSecretAdapter{}).Delete(context.Background(), store, kv) }); err != nil {
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
			kv.Status.ManagedKeys, kv.Status.Acquired, kv.Status.PathCreated = []string{"username"}, tt.acquired, true
			kv.Annotations = map[string]string{commonv1alpha1.AnnotationAllowDestructiveDelete: tt.annotation}
			if err := finishKVDelete(func() error { return (KVSecretAdapter{}).Delete(context.Background(), store, kv) }); err != nil {
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

// TestRandomSecretPurgesOnlyAPathItCreated: a RandomSecret that added its key to a path with earlier versions removes
// only its key on delete; the history goes only with a path the resource created.
func TestRandomSecretPurgesOnlyAPathItCreated(t *testing.T) {
	for name, tt := range map[string]struct {
		versions []map[string]any
		purge    bool
	}{
		"existing history": {versions: []map[string]any{{"old": "o"}, {}}},
		"new path":         {purge: true},
	} {
		t.Run(name, func(t *testing.T) {
			store := &kvStore{versions: tt.versions}
			s := newRandomSecret()
			s.Annotations = map[string]string{commonv1alpha1.AnnotationAllowDestructiveDelete: "true"}
			a := RandomSecretAdapter{}
			if _, found, err := a.Observe(context.Background(), store, s); err != nil || found {
				t.Fatalf("observe: found=%v err=%v, want absent", found, err)
			}
			if _, err := a.Write(context.Background(), store, s, nil, true); err != nil {
				t.Fatal(err)
			}
			s.Status.Acquired = commonv1alpha1.AcquiredCreated
			if err := finishKVDelete(func() error { return a.Delete(context.Background(), store, s) }); err != nil {
				t.Fatal(err)
			}
			if store.purged != tt.purge {
				t.Errorf("purged = %v, want %v", store.purged, tt.purge)
			}
			if !tt.purge && (len(store.versions) != len(tt.versions)+2 || len(store.latest()) != 0) {
				t.Errorf("versions = %d, want the history plus the generated and an empty version", len(store.versions))
			}
		})
	}
}

// TestKVSecretPurgesOnlyAPathItCreated: a KVSecret written to a path with earlier versions but no live keys removes
// only its keys on delete; the history goes only with a path the resource created.
func TestKVSecretPurgesOnlyAPathItCreated(t *testing.T) {
	for name, tt := range map[string]struct {
		versions []map[string]any
		purge    bool
	}{
		"existing history": {versions: []map[string]any{{"old": "o"}, {}}},
		"new path":         {purge: true},
	} {
		t.Run(name, func(t *testing.T) {
			store := &kvStore{versions: tt.versions}
			s := newKVSecret(2)
			s.Annotations = map[string]string{commonv1alpha1.AnnotationAllowDestructiveDelete: "true"}
			a := KVSecretAdapter{}
			if _, found, err := a.Observe(context.Background(), store, s); err != nil || found {
				t.Fatalf("observe: found=%v err=%v, want absent", found, err)
			}
			desired := map[string]any{kvValuesKey: map[string]any{"username": "u"}}
			if _, err := a.Write(context.Background(), store, s, desired, true); err != nil {
				t.Fatal(err)
			}
			s.Status.Acquired = commonv1alpha1.AcquiredCreated
			if err := finishKVDelete(func() error { return a.Delete(context.Background(), store, s) }); err != nil {
				t.Fatal(err)
			}
			if store.purged != tt.purge {
				t.Errorf("purged = %v, want %v", store.purged, tt.purge)
			}
			if !tt.purge && (len(store.versions) != len(tt.versions)+2 || len(store.latest()) != 0) {
				t.Errorf("versions = %d, want the history plus the written and an empty version", len(store.versions))
			}
		})
	}
}

func finishKVDelete(remove func() error) error {
	err := remove()
	if errors.Is(err, engine.ErrInProgress) {
		return remove()
	}
	return err
}

func TestKVHistoryCutoffSurvivesPartialFailure(t *testing.T) {
	store := &kvStore{versions: make([]map[string]any, 130)}
	for i := range store.versions {
		store.versions[i] = map[string]any{"username": "u"}
	}
	kv := newKVSecret(2)
	kv.Status.ManagedKeys, kv.Status.Acquired, kv.Status.PathCreated = []string{"username"}, commonv1alpha1.AcquiredCreated, true
	kv.Annotations = map[string]string{commonv1alpha1.AnnotationAllowDestructiveDelete: "true"}
	adapter := KVSecretAdapter{}
	if err := adapter.Delete(t.Context(), store, kv); !errors.Is(err, engine.ErrInProgress) {
		t.Fatalf("checkpoint: %v", err)
	}
	if store.purged || kv.Status.PurgeThroughVersion == nil || *kv.Status.PurgeThroughVersion != 131 {
		t.Fatal("destroyed before checkpoint or invalid cutoff")
	}
	// Model a fresh pass with only the persisted API object, not adapter memory.
	kv = kv.DeepCopy()
	attempts := 0
	store.beforeDestroy = func(s *kvStore) error {
		attempts++
		if attempts == 1 {
			s.versions = append(s.versions, map[string]any{"username": "new", "other": "preserve"})
		}
		if attempts == 2 {
			return errors.New("temporary failure")
		}
		return nil
	}
	if err := adapter.Delete(t.Context(), store, kv); err == nil {
		t.Fatal("partial failure ignored")
	}
	if err := adapter.Delete(t.Context(), store, kv.DeepCopy()); err != nil {
		t.Fatal(err)
	}
	if *kv.Status.PurgeThroughVersion != 131 || len(store.destroyed) != 131 || !reflect.DeepEqual(store.latest(), map[string]any{"username": "new", "other": "preserve"}) {
		t.Fatal("retry destroyed a newer version or widened cutoff")
	}
	for _, n := range []int64{0, -1, 1000} {
		kv.Status.PurgeThroughVersion = &n
		if err := adapter.Delete(t.Context(), store, kv); err == nil {
			t.Fatal("invalid cutoff accepted")
		}
	}
}
