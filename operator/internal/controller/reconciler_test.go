package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/model"
	v1 "github.com/manojrevenkar12-web/vector-db-lifecycle-operator/operator/api/v1alpha1"
)

type fakeDB struct {
	peer   uint64
	shards *[]model.Shard
	calls  *[]model.Action
}

func (f fakeDB) PeerID(context.Context) (uint64, error) { return f.peer, nil }
func (f fakeDB) Shards(context.Context) ([]model.Shard, []model.Transfer, error) {
	return *f.shards, nil, nil
}
func (f fakeDB) Execute(_ context.Context, a model.Action) error {
	*f.calls = append(*f.calls, a)
	return nil
}
func (f fakeDB) RemovePeer(context.Context, uint64) error { return nil }

func setup(t *testing.T) (client.Client, *Reconciler, *[]model.Shard, *[]model.Action) {
	t.Helper()
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = v1.AddToScheme(s)
	vc := &v1.VectorCluster{ObjectMeta: metav1.ObjectMeta{Name: "vec", Namespace: "db", UID: "u1"}, Spec: v1.VectorClusterSpec{Version: "v1.15.0", Nodes: 3, ReplicationFactor: 2}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(vc).WithStatusSubresource(vc).Build()
	shards := &[]model.Shard{}
	calls := &[]model.Action{}
	r := &Reconciler{Client: c, Busy: 1, Idle: 1, Connect: func(_ *v1.VectorCluster, o int) Database {
		return fakeDB{peer: uint64(o + 1), shards: shards, calls: calls}
	}}
	return c, r, shards, calls
}

func reconcile(t *testing.T, r *Reconciler) {
	t.Helper()
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "db", Name: "vec"}}); err != nil {
		t.Fatal(err)
	}
}

func markReady(t *testing.T, c client.Client) {
	t.Helper()
	var pods corev1.PodList
	_ = c.List(context.Background(), &pods, client.InNamespace("db"))
	for i := range pods.Items {
		p := &pods.Items[i]
		p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
		if err := c.Update(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
}

func get(t *testing.T, c client.Client) v1.VectorCluster {
	var vc v1.VectorCluster
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "db", Name: "vec"}, &vc); err != nil {
		t.Fatal(err)
	}
	return vc
}

func TestProvisionThenRollingUpgradeOneNodeAtATime(t *testing.T) {
	c, r, shards, _ := setup(t)
	reconcile(t, r)
	var pods corev1.PodList
	_ = c.List(context.Background(), &pods, client.InNamespace("db"))
	if len(pods.Items) != 3 {
		t.Fatalf("want 3 pods, got %d", len(pods.Items))
	}
	for _, p := range pods.Items {
		if p.Spec.Containers[0].Image != "qdrant/qdrant:v1.15.0" || p.Spec.Subdomain != "vec-headless" {
			t.Fatalf("%s: %s %s", p.Name, p.Spec.Containers[0].Image, p.Spec.Subdomain)
		}
	}
	markReady(t, c)
	*shards = []model.Shard{
		{Collection: "c", ID: 0, Replicas: []model.Replica{{Peer: 1, State: model.Active}, {Peer: 3, State: model.Active}}},
		{Collection: "c", ID: 1, Replicas: []model.Replica{{Peer: 2, State: model.Active}, {Peer: 3, State: model.Active}}},
	}
	reconcile(t, r)
	if vc := get(t, c); vc.Status.Phase != string(model.Healthy) || vc.Status.ReadyNodes != 3 {
		t.Fatalf("%+v", vc.Status)
	}

	vc := get(t, c)
	vc.Spec.Version = "v1.16.0"
	if err := c.Update(context.Background(), &vc); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r)
	pods = corev1.PodList{}
	_ = c.List(context.Background(), &pods, client.InNamespace("db"))
	if len(pods.Items) != 2 {
		t.Fatalf("one node restarts at a time: %d pods left", len(pods.Items))
	}
	for _, p := range pods.Items {
		if p.Name == "vec-2" {
			t.Fatal("the highest ordinal goes first")
		}
	}
	var claim corev1.PersistentVolumeClaim
	_ = c.Get(context.Background(), types.NamespacedName{Namespace: "db", Name: "storage-vec-2"}, &claim)
	if claim.Annotations[AnnoVersion] != "v1.16.0" {
		t.Fatalf("the volume keeps the version to run: %v", claim.Annotations)
	}
	reconcile(t, r) // the pod comes back at the new version
	var p corev1.Pod
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "db", Name: "vec-2"}, &p); err != nil {
		t.Fatal(err)
	}
	if p.Spec.Containers[0].Image != "qdrant/qdrant:v1.16.0" {
		t.Fatal(p.Spec.Containers[0].Image)
	}
	if got := get(t, c).Status.Phase; got != string(model.Waiting) {
		t.Fatalf("wait while the node is down: %s", got)
	}
}

func TestScaleDownDrainsFirst(t *testing.T) {
	c, r, shards, calls := setup(t)
	reconcile(t, r)
	markReady(t, c)
	*shards = []model.Shard{{Collection: "c", ID: 0, Replicas: []model.Replica{{Peer: 1, State: model.Active}, {Peer: 3, State: model.Active}}}}
	reconcile(t, r)
	vc := get(t, c)
	vc.Spec.Nodes = 2
	_ = c.Update(context.Background(), &vc)
	reconcile(t, r)
	if len(*calls) != 1 || (*calls)[0].Kind != model.ReplicateShard || (*calls)[0].To != 2 {
		t.Fatalf("the only other copy is on the node going away: move it first: %+v", *calls)
	}
	var p corev1.Pod
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "db", Name: "vec-2"}, &p); err != nil {
		t.Fatal("the node must stay until it is drained")
	}
}

func TestBootstrapJoinsARunningPeer(t *testing.T) {
	vc := &v1.VectorCluster{ObjectMeta: metav1.ObjectMeta{Name: "vec", Namespace: "db"}}
	if a := Args(vc, 0, -1); len(a) != 2 {
		t.Fatal(a)
	}
	if a := Args(vc, 2, 0); a[3] != "http://vec-0.vec-headless.db.svc:6335" {
		t.Fatal(a)
	}
	ns := []*node{{ordinal: 0}, {ordinal: 1, ready: true}}
	if bootstrapFor(ns, 0) != 1 || bootstrapFor([]*node{{ordinal: 0}}, 2) != 0 || bootstrapFor(nil, 0) != -1 {
		t.Fatal("bootstrap choice")
	}
}
