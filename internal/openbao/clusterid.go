package openbao

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/openbao/openbao/api/v2"
)

// clusterIDTTL bounds how long a session trusts the cluster id it read, so a different server behind the same
// address (a DNS or load balancer change, a re-initialised server) is noticed within that time.
const clusterIDTTL = time.Minute

// ClusterID returns the cluster_id that sys/health reports without a token. It is created when the cluster is
// initialised and is the same on every node and after restarts. The session caches it for clusterIDTTL.
func (c *client) ClusterID(ctx context.Context) (string, error) {
	c.s.idMu.Lock()
	id, readAt := c.s.clusterID, c.s.clusterIDAt
	c.s.idMu.Unlock()
	if id != "" && c.s.now().Sub(readAt) < clusterIDTTL {
		return id, nil
	}
	return c.FreshClusterID(ctx)
}

// FreshClusterID reads the cluster id from sys/health, bypassing the cache, and caches the result. The engine calls
// it right before a create or a delete.
func (c *client) FreshClusterID(ctx context.Context) (string, error) {
	var health map[string]any
	err := c.s.do(ctx, "", func(ac *api.Client) error {
		var err error
		health, err = getJSON(ctx, ac, "sys/health", url.Values{"standbyok": {queryTrue}, "perfstandbyok": {queryTrue}})
		return err
	})
	if err != nil {
		return "", err
	}
	id, _ := health["cluster_id"].(string)
	if id == "" {
		return "", errors.New("openbao: sys/health reported no cluster_id")
	}
	c.s.idMu.Lock()
	c.s.clusterID, c.s.clusterIDAt = id, c.s.now()
	c.s.idMu.Unlock()
	return id, nil
}
