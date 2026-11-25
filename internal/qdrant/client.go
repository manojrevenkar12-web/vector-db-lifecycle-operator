// Package qdrant reads a vector database cluster's state and carries out the planner's data steps over its REST API:
// which peer each node is, where every shard's replicas are and in what state, transfers in flight, snapshots, and
// replicating or dropping a shard replica. Standard library only, so it is tested without a cluster.
package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/model"
)

// Client talks to one node.
type Client struct {
	Base   string // http://host:6333
	APIKey string
	HTTP   *http.Client
}

// New returns a client with a short timeout: a reconcile pass must not hang on one node.
func New(base, apiKey string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), APIKey: apiKey, HTTP: &http.Client{Timeout: 5 * time.Second}}
}

type envelope struct {
	Result json.RawMessage `json:"result"`
	Status any             `json:"status"`
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("api-key", c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	return json.Unmarshal(env.Result, out)
}

// PeerID is the peer this node is in the cluster.
func (c *Client) PeerID(ctx context.Context) (uint64, error) {
	var r struct {
		PeerID uint64 `json:"peer_id"`
		Status string `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, "/cluster", nil, &r); err != nil {
		return 0, err
	}
	if r.Status != "enabled" {
		return 0, fmt.Errorf("distributed mode is %q on %s", r.Status, c.Base)
	}
	return r.PeerID, nil
}

type shardInfo struct {
	ShardID int    `json:"shard_id"`
	PeerID  uint64 `json:"peer_id"`
	State   string `json:"state"`
}

type collectionCluster struct {
	PeerID         uint64      `json:"peer_id"`
	LocalShards    []shardInfo `json:"local_shards"`
	RemoteShards   []shardInfo `json:"remote_shards"`
	ShardTransfers []struct {
		ShardID int    `json:"shard_id"`
		From    uint64 `json:"from"`
		To      uint64 `json:"to"`
	} `json:"shard_transfers"`
}

// state maps the database's replica states onto the planner's.
func state(s string) model.ReplicaState {
	switch s {
	case "Active", "ReshardingScaleDown":
		return model.Active
	case "Dead":
		return model.Dead
	case "Initializing", "Listener":
		return model.Initializing
	default: // Partial, PartialSnapshot, Recovery, Resharding
		return model.Partial
	}
}

// Shards returns every collection's shards, their replicas and the transfers in flight, as one node sees them.
func (c *Client) Shards(ctx context.Context) ([]model.Shard, []model.Transfer, error) {
	var cols struct {
		Collections []struct {
			Name string `json:"name"`
		} `json:"collections"`
	}
	if err := c.do(ctx, http.MethodGet, "/collections", nil, &cols); err != nil {
		return nil, nil, err
	}
	var shards []model.Shard
	var moves []model.Transfer
	for _, col := range cols.Collections {
		var cc collectionCluster
		if err := c.do(ctx, http.MethodGet, "/collections/"+url.PathEscape(col.Name)+"/cluster", nil, &cc); err != nil {
			return nil, nil, err
		}
		byID := map[int]*model.Shard{}
		add := func(si shardInfo, peer uint64) {
			s, ok := byID[si.ShardID]
			if !ok {
				s = &model.Shard{Collection: col.Name, ID: si.ShardID}
				byID[si.ShardID] = s
			}
			s.Replicas = append(s.Replicas, model.Replica{Peer: peer, State: state(si.State)})
		}
		for _, si := range cc.LocalShards {
			add(si, cc.PeerID)
		}
		for _, si := range cc.RemoteShards {
			add(si, si.PeerID)
		}
		ids := make([]int, 0, len(byID))
		for id := range byID {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, id := range ids {
			s := byID[id]
			sort.Slice(s.Replicas, func(i, j int) bool { return s.Replicas[i].Peer < s.Replicas[j].Peer })
			shards = append(shards, *s)
		}
		for _, t := range cc.ShardTransfers {
			moves = append(moves, model.Transfer{Collection: col.Name, Shard: t.ShardID, From: t.From, To: t.To})
		}
	}
	return shards, moves, nil
}

// Snapshots lists the node's full snapshots as backups (taken, not yet restore-tested).
func (c *Client) Snapshots(ctx context.Context, now time.Time) ([]model.Backup, error) {
	var snaps []struct {
		Name         string `json:"name"`
		CreationTime string `json:"creation_time"`
	}
	if err := c.do(ctx, http.MethodGet, "/snapshots", nil, &snaps); err != nil {
		return nil, err
	}
	var out []model.Backup
	for _, s := range snaps {
		t, err := time.Parse("2006-01-02T15:04:05", s.CreationTime)
		if err != nil {
			continue
		}
		out = append(out, model.Backup{ID: s.Name, At: -int(now.Sub(t).Minutes()), OK: true})
	}
	return out, nil
}

// Execute carries out a data step on the database. Node steps (create, restart, delete) belong to the platform.
func (c *Client) Execute(ctx context.Context, a model.Action) error {
	col := "/collections/" + url.PathEscape(a.Collection) + "/cluster"
	switch a.Kind {
	case model.ReplicateShard:
		return c.do(ctx, http.MethodPost, col, map[string]any{"replicate_shard": map[string]any{"shard_id": a.Shard, "from_peer_id": a.From, "to_peer_id": a.To}}, nil)
	case model.DropReplica:
		return c.do(ctx, http.MethodPost, col, map[string]any{"drop_replica": map[string]any{"shard_id": a.Shard, "peer_id": a.From}}, nil)
	case model.TakeBackup:
		return c.do(ctx, http.MethodPost, "/snapshots?wait=false", nil, nil)
	case model.DeleteBackup:
		return c.do(ctx, http.MethodDelete, "/snapshots/"+url.PathEscape(a.Backup), nil, nil)
	default:
		return fmt.Errorf("%s is not a database step", a.Kind)
	}
}

// RemovePeer takes an empty peer out of the cluster before its node is deleted.
func (c *Client) RemovePeer(ctx context.Context, peer uint64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/cluster/peer/%d", peer), nil, nil)
}
