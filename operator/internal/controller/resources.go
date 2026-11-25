// Package controller maps a VectorCluster onto pods, volumes and the database's own cluster API, and lets the shared planner decide.
package controller

import (
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	v1 "github.com/manojrevenkar12-web/vector-db-lifecycle-operator/operator/api/v1alpha1"
)

const (
	LabelCluster  = "lifecycle.vectordb.dev/cluster"
	LabelOrdinal  = "lifecycle.vectordb.dev/ordinal"
	AnnoVersion   = "lifecycle.vectordb.dev/version"
	DefaultImage  = "qdrant/qdrant"
	DefaultSize   = "1Gi"
	HTTPPort      = 6333
	GRPCPort      = 6334
	P2PPort       = 6335
	volumeName    = "storage"
	containerName = "database"
)

// HeadlessName is the Service that gives every node a stable DNS name.
func HeadlessName(vc *v1.VectorCluster) string { return vc.Name + "-headless" }

// NodeName is the pod name of the node with this ordinal.
func NodeName(vc *v1.VectorCluster, ordinal int) string {
	return fmt.Sprintf("%s-%d", vc.Name, ordinal)
}

// ClaimName is the volume of the node with this ordinal; it outlives restarts.
func ClaimName(vc *v1.VectorCluster, ordinal int) string { return "storage-" + NodeName(vc, ordinal) }

// Host is the node's stable DNS name inside the cluster.
func Host(vc *v1.VectorCluster, ordinal int) string {
	return fmt.Sprintf("%s.%s.%s.svc", NodeName(vc, ordinal), HeadlessName(vc), vc.Namespace)
}

// BaseURL is the node's REST endpoint.
func BaseURL(vc *v1.VectorCluster, ordinal int) string {
	return fmt.Sprintf("http://%s:%d", Host(vc, ordinal), HTTPPort)
}

// Image is the image for a version.
func Image(vc *v1.VectorCluster, version string) string {
	repo := vc.Spec.Image
	if repo == "" {
		repo = DefaultImage
	}
	return repo + ":" + version
}

func labels(vc *v1.VectorCluster, ordinal int) map[string]string {
	l := map[string]string{LabelCluster: vc.Name, "app.kubernetes.io/name": "vector-db", "app.kubernetes.io/managed-by": "vector-db-lifecycle-operator"}
	if ordinal >= 0 {
		l[LabelOrdinal] = strconv.Itoa(ordinal)
	}
	return l
}

func owner(vc *v1.VectorCluster) []metav1.OwnerReference {
	t := true
	return []metav1.OwnerReference{{APIVersion: v1.GroupVersion.String(), Kind: "VectorCluster", Name: vc.Name, UID: vc.UID, Controller: &t, BlockOwnerDeletion: &t}}
}

// Ordinal reads a resource's ordinal label.
func Ordinal(m metav1.Object) (int, bool) {
	v, ok := m.GetLabels()[LabelOrdinal]
	if !ok {
		return 0, false
	}
	i, err := strconv.Atoi(v)
	return i, err == nil && i >= 0
}

// Headless is the Service for peer DNS. Not-ready addresses are published so peers can find each other while they join.
func Headless(vc *v1.VectorCluster) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: HeadlessName(vc), Namespace: vc.Namespace, Labels: labels(vc, -1), OwnerReferences: owner(vc)},
		Spec: corev1.ServiceSpec{
			ClusterIP:                corev1.ClusterIPNone,
			PublishNotReadyAddresses: true,
			Selector:                 map[string]string{LabelCluster: vc.Name},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: HTTPPort, TargetPort: intstr.FromInt32(HTTPPort)},
				{Name: "grpc", Port: GRPCPort, TargetPort: intstr.FromInt32(GRPCPort)},
				{Name: "p2p", Port: P2PPort, TargetPort: intstr.FromInt32(P2PPort)},
			},
		},
	}
}

// ClientService is what applications connect to: ready nodes only.
func ClientService(vc *v1.VectorCluster) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: vc.Name, Namespace: vc.Namespace, Labels: labels(vc, -1), OwnerReferences: owner(vc)},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{LabelCluster: vc.Name},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: HTTPPort, TargetPort: intstr.FromInt32(HTTPPort)},
				{Name: "grpc", Port: GRPCPort, TargetPort: intstr.FromInt32(GRPCPort)},
			},
		},
	}
}

// Claim is a node's volume. The version it should run is kept on the claim, so a restart is "set the version, delete the pod".
func Claim(vc *v1.VectorCluster, ordinal int, version string) (*corev1.PersistentVolumeClaim, error) {
	size := vc.Spec.StorageSize
	if size == "" {
		size = DefaultSize
	}
	q, err := resource.ParseQuantity(size)
	if err != nil {
		return nil, fmt.Errorf("storageSize %q: %w", size, err)
	}
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: ClaimName(vc, ordinal), Namespace: vc.Namespace, Labels: labels(vc, ordinal), Annotations: map[string]string{AnnoVersion: version}, OwnerReferences: owner(vc)},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: vc.Spec.StorageClassName,
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: q}},
		},
	}, nil
}

// Args are the database flags: its own URI, and for every node but the first, a peer to join through.
func Args(vc *v1.VectorCluster, ordinal int, bootstrap int) []string {
	args := []string{"--uri", fmt.Sprintf("http://%s:%d", Host(vc, ordinal), P2PPort)}
	if bootstrap >= 0 && bootstrap != ordinal {
		args = append(args, "--bootstrap", fmt.Sprintf("http://%s:%d", Host(vc, bootstrap), P2PPort))
	}
	return args
}

// Pod is one database node. bootstrap is the ordinal of a running peer to join through, or -1 for the first node.
func Pod(vc *v1.VectorCluster, ordinal int, version string, bootstrap int) *corev1.Pod {
	probe := func(path string) *corev1.Probe {
		return &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt32(HTTPPort)}}, PeriodSeconds: 3, FailureThreshold: 3}
	}
	cmd := "exec ./entrypoint.sh " + strings.Join(Args(vc, ordinal, bootstrap), " ")
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: NodeName(vc, ordinal), Namespace: vc.Namespace, Labels: labels(vc, ordinal), Annotations: map[string]string{AnnoVersion: version}, OwnerReferences: owner(vc)},
		Spec: corev1.PodSpec{
			Hostname:                      NodeName(vc, ordinal),
			Subdomain:                     HeadlessName(vc),
			TerminationGracePeriodSeconds: ptr(int64(30)),
			Containers: []corev1.Container{{
				Name:    containerName,
				Image:   Image(vc, version),
				Command: []string{"/bin/bash", "-c", cmd},
				Env:     []corev1.EnvVar{{Name: "QDRANT__CLUSTER__ENABLED", Value: "true"}},
				Ports: []corev1.ContainerPort{
					{Name: "http", ContainerPort: HTTPPort},
					{Name: "grpc", ContainerPort: GRPCPort},
					{Name: "p2p", ContainerPort: P2PPort},
				},
				ReadinessProbe: probe("/readyz"),
				LivenessProbe:  &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/livez", Port: intstr.FromInt32(HTTPPort)}}, PeriodSeconds: 10, FailureThreshold: 6, InitialDelaySeconds: 10},
				VolumeMounts:   []corev1.VolumeMount{{Name: volumeName, MountPath: "/qdrant/storage"}},
			}},
			Volumes: []corev1.Volume{{Name: volumeName, VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: ClaimName(vc, ordinal)}}}},
		},
	}
}

// PodReady reports the pod's Ready condition and when it last changed.
func PodReady(p *corev1.Pod) (bool, metav1.Time) {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue, c.LastTransitionTime
		}
	}
	return false, p.CreationTimestamp
}

func ptr[T any](v T) *T { return &v }
