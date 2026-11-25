package controller

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/model"
	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/plan"
	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/qdrant"
	v1 "github.com/manojrevenkar12-web/vector-db-lifecycle-operator/operator/api/v1alpha1"
)

// Database is the part of the database's API the operator uses; tests swap it.
type Database interface {
	PeerID(ctx context.Context) (uint64, error)
	Shards(ctx context.Context) ([]model.Shard, []model.Transfer, error)
	Execute(ctx context.Context, a model.Action) error
	RemovePeer(ctx context.Context, peer uint64) error
}

// Reconciler turns a VectorCluster into pods and volumes, observes the database and runs the shared planner's decision.
type Reconciler struct {
	client.Client
	// Connect returns the database API of one node. The default speaks REST to the node's stable DNS name.
	Connect func(vc *v1.VectorCluster, ordinal int) Database
	// Options are the planner's safeguards; the default is plan.Safe().
	Options *plan.Options
	// Busy and Idle are how soon to look again while a change is running and when the cluster is healthy.
	Busy, Idle time.Duration
}

// +kubebuilder:rbac:groups=lifecycle.vectordb.dev,resources=vectorclusters;vectorclusters/status,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=pods;persistentvolumeclaims;services,verbs=get;list;watch;create;update;patch;delete

// SetupWithManager registers the controller.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Connect == nil {
		r.Connect = func(vc *v1.VectorCluster, ordinal int) Database { return qdrant.New(BaseURL(vc, ordinal), "") }
	}
	if r.Busy == 0 {
		r.Busy = 3 * time.Second
	}
	if r.Idle == 0 {
		r.Idle = 30 * time.Second
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.VectorCluster{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Complete(r)
}

type node struct {
	ordinal int
	claim   *corev1.PersistentVolumeClaim
	pod     *corev1.Pod
	ready   bool
	since   time.Time
}

// Reconcile observes, plans once, acts, and records what it did.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	var vc v1.VectorCluster
	if err := r.Get(ctx, req.NamespacedName, &vc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !vc.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	if err := r.ensureServices(ctx, &vc); err != nil {
		return ctrl.Result{}, err
	}
	nodes, err := r.nodes(ctx, &vc)
	if err != nil {
		return ctrl.Result{}, err
	}
	// The volume carries the version a node should run, so a missing pod is put back as it was meant to be.
	for _, n := range nodes {
		if n.pod == nil && n.claim.DeletionTimestamp.IsZero() {
			if err := r.Create(ctx, Pod(&vc, n.ordinal, n.claim.Annotations[AnnoVersion], bootstrapFor(nodes, n.ordinal))); err != nil && !apierrors.IsAlreadyExists(err) {
				return ctrl.Result{}, err
			}
		}
	}
	if vc.Status.Peers == nil {
		vc.Status.Peers = map[string]uint64{}
	}
	obs, db := r.observe(ctx, &vc, nodes)
	spec := model.Spec{Version: vc.Spec.Version, Nodes: vc.Spec.Nodes, ReplicationFactor: vc.Spec.ReplicationFactor}
	opt := plan.Safe()
	if r.Options != nil {
		opt = *r.Options
	}
	// Backups and DR are planned in the lab only; this adapter does not run them yet.
	opt.VerifyBackups, opt.ShipBackups = false, false
	d := plan.Plan(spec, obs, opt)

	var done []string
	var errs []error
	for _, a := range d.Actions {
		if err := r.act(ctx, &vc, nodes, db, obs, a); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.Kind, err))
			logger.Error(err, "step failed", "kind", a.Kind, "node", a.Node, "shard", a.Shard)
			continue
		}
		done = append(done, describe(a))
	}

	vc.Status.Phase, vc.Status.Message = string(d.Phase), d.Message
	vc.Status.LastActions = done
	vc.Status.ObservedGeneration = vc.Generation
	vc.Status.Nodes, vc.Status.ReadyNodes = nil, 0
	for _, n := range obs.Nodes {
		vc.Status.Nodes = append(vc.Status.Nodes, v1.NodeStatus{Name: NodeName(&vc, n.Ordinal), Ordinal: n.Ordinal, Peer: n.Peer, Version: n.Version, Ready: n.Ready})
		if n.Ready {
			vc.Status.ReadyNodes++
		}
	}
	cond := metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: string(d.Phase), Message: d.Message, ObservedGeneration: vc.Generation}
	if d.Phase == model.Healthy && len(errs) == 0 {
		cond.Status = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&vc.Status.Conditions, cond)
	if err := r.Status().Update(ctx, &vc); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	if len(errs) > 0 {
		return ctrl.Result{RequeueAfter: r.Busy}, nil // the next look re-plans from what the database now reports
	}
	if d.Phase == model.Healthy {
		return ctrl.Result{RequeueAfter: r.Idle}, nil
	}
	return ctrl.Result{RequeueAfter: r.Busy}, nil
}

func (r *Reconciler) ensureServices(ctx context.Context, vc *v1.VectorCluster) error {
	for _, s := range []*corev1.Service{Headless(vc), ClientService(vc)} {
		if err := r.Create(ctx, s); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
	}
	return nil
}

func (r *Reconciler) nodes(ctx context.Context, vc *v1.VectorCluster) ([]*node, error) {
	sel := client.MatchingLabels{LabelCluster: vc.Name}
	var claims corev1.PersistentVolumeClaimList
	if err := r.List(ctx, &claims, client.InNamespace(vc.Namespace), sel); err != nil {
		return nil, err
	}
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(vc.Namespace), sel); err != nil {
		return nil, err
	}
	byOrd := map[int]*node{}
	for i := range claims.Items {
		c := &claims.Items[i]
		if o, ok := Ordinal(c); ok {
			byOrd[o] = &node{ordinal: o, claim: c}
		}
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		o, ok := Ordinal(p)
		if !ok || byOrd[o] == nil {
			continue
		}
		n := byOrd[o]
		n.pod = p
		ready, since := PodReady(p)
		n.ready = ready && p.DeletionTimestamp.IsZero()
		n.since = since.Time
		if !p.DeletionTimestamp.IsZero() {
			n.since = p.DeletionTimestamp.Time
		}
	}
	out := make([]*node, 0, len(byOrd))
	for _, n := range byOrd {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ordinal < out[j].ordinal })
	return out, nil
}

// bootstrapFor picks the peer a node joins through: any ready node, else the first node; the first node of a new cluster joins nobody.
func bootstrapFor(nodes []*node, self int) int {
	for _, n := range nodes {
		if n.ordinal != self && n.ready {
			return n.ordinal
		}
	}
	if self != 0 {
		return 0
	}
	return -1
}

// version is what the node runs: the pod's version while it exists, else the version its volume asks for.
func (n *node) version() string {
	if n.pod != nil {
		if v := n.pod.Annotations[AnnoVersion]; v != "" {
			return v
		}
	}
	return n.claim.Annotations[AnnoVersion]
}

// observe builds what the planner sees. A node that is down keeps the peer id it had, from the status.
func (r *Reconciler) observe(ctx context.Context, vc *v1.VectorCluster, nodes []*node) (model.Observed, Database) {
	now := time.Now()
	obs := model.Observed{Now: int(now.Sub(vc.CreationTimestamp.Time).Minutes())}
	var db Database
	for _, n := range nodes {
		key := strconv.Itoa(n.ordinal)
		if n.ready {
			c := r.Connect(vc, n.ordinal)
			if id, err := c.PeerID(ctx); err == nil {
				vc.Status.Peers[key] = id
				if db == nil {
					db = c
				}
			} else {
				n.ready = false
			}
		}
		mn := model.Node{Name: plan.NodeName(n.ordinal), Ordinal: n.ordinal, Peer: vc.Status.Peers[key], Version: n.version(), Ready: n.ready}
		if !n.ready && !n.since.IsZero() {
			mn.DownFor = int(now.Sub(n.since).Minutes())
		}
		obs.Nodes = append(obs.Nodes, mn)
	}
	if db != nil {
		if shards, moves, err := db.Shards(ctx); err == nil {
			obs.Shards, obs.Transfers = shards, moves
		}
	}
	return obs, db
}

func (r *Reconciler) act(ctx context.Context, vc *v1.VectorCluster, nodes []*node, db Database, obs model.Observed, a model.Action) error {
	find := func(name string) *node {
		for _, n := range nodes {
			if plan.NodeName(n.ordinal) == name {
				return n
			}
		}
		return nil
	}
	switch a.Kind {
	case model.CreateNode:
		c, err := Claim(vc, a.Ordinal, a.Version)
		if err != nil {
			return err
		}
		if err := r.Create(ctx, c); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
		if err := r.Create(ctx, Pod(vc, a.Ordinal, a.Version, bootstrapFor(nodes, a.Ordinal))); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
		return nil
	case model.RestartNode:
		n := find(a.Node)
		if n == nil || n.pod == nil {
			return errors.New("node not found")
		}
		patch := client.MergeFrom(n.claim.DeepCopy())
		if n.claim.Annotations == nil {
			n.claim.Annotations = map[string]string{}
		}
		n.claim.Annotations[AnnoVersion] = a.Version
		if err := r.Patch(ctx, n.claim, patch); err != nil {
			return err
		}
		// The volume stays; the pod comes back at the new version on the next look.
		return client.IgnoreNotFound(r.Delete(ctx, n.pod))
	case model.DeleteNode:
		n := find(a.Node)
		if n == nil {
			return nil
		}
		if db != nil {
			for _, on := range obs.Nodes {
				if on.Name == a.Node && on.Peer != 0 {
					if err := db.RemovePeer(ctx, on.Peer); err != nil {
						return fmt.Errorf("remove peer %d: %w", on.Peer, err)
					}
					delete(vc.Status.Peers, strconv.Itoa(n.ordinal))
				}
			}
		}
		if n.pod != nil {
			if err := client.IgnoreNotFound(r.Delete(ctx, n.pod)); err != nil {
				return err
			}
		}
		return client.IgnoreNotFound(r.Delete(ctx, n.claim))
	case model.ReplicateShard, model.DropReplica:
		if db == nil {
			return errors.New("no node answers")
		}
		return db.Execute(ctx, a)
	default:
		// TakeBackup, VerifyBackup, ShipBackup, DeleteBackup, RestoreShard: planned in the lab, not run by this adapter.
		return nil
	}
}

func describe(a model.Action) string {
	switch a.Kind {
	case model.ReplicateShard:
		return fmt.Sprintf("%s %s/%d %d→%d", a.Kind, a.Collection, a.Shard, a.From, a.To)
	case model.DropReplica:
		return fmt.Sprintf("%s %s/%d from %d", a.Kind, a.Collection, a.Shard, a.From)
	default:
		return fmt.Sprintf("%s %s %s", a.Kind, a.Node, a.Version)
	}
}
