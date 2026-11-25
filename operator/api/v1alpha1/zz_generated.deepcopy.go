// Code written to the controller-gen deepcopy pattern; keep in step with types.go.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func (in *DRSpec) DeepCopyInto(out *DRSpec) { *out = *in }

func (in *DRSpec) DeepCopy() *DRSpec {
	if in == nil {
		return nil
	}
	out := new(DRSpec)
	in.DeepCopyInto(out)
	return out
}

func (in *VectorClusterSpec) DeepCopyInto(out *VectorClusterSpec) {
	*out = *in
	if in.StorageClassName != nil {
		v := *in.StorageClassName
		out.StorageClassName = &v
	}
	if in.DR != nil {
		out.DR = in.DR.DeepCopy()
	}
}

func (in *VectorClusterSpec) DeepCopy() *VectorClusterSpec {
	if in == nil {
		return nil
	}
	out := new(VectorClusterSpec)
	in.DeepCopyInto(out)
	return out
}

func (in *NodeStatus) DeepCopyInto(out *NodeStatus) { *out = *in }

func (in *VectorClusterStatus) DeepCopyInto(out *VectorClusterStatus) {
	*out = *in
	if in.LastActions != nil {
		out.LastActions = append([]string(nil), in.LastActions...)
	}
	if in.Nodes != nil {
		out.Nodes = append([]NodeStatus(nil), in.Nodes...)
	}
	if in.Peers != nil {
		out.Peers = make(map[string]uint64, len(in.Peers))
		for k, v := range in.Peers {
			out.Peers[k] = v
		}
	}
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}

func (in *VectorClusterStatus) DeepCopy() *VectorClusterStatus {
	if in == nil {
		return nil
	}
	out := new(VectorClusterStatus)
	in.DeepCopyInto(out)
	return out
}

func (in *VectorCluster) DeepCopyInto(out *VectorCluster) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *VectorCluster) DeepCopy() *VectorCluster {
	if in == nil {
		return nil
	}
	out := new(VectorCluster)
	in.DeepCopyInto(out)
	return out
}

func (in *VectorCluster) DeepCopyObject() runtime.Object { return in.DeepCopy() }

func (in *VectorClusterList) DeepCopyInto(out *VectorClusterList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]VectorCluster, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *VectorClusterList) DeepCopy() *VectorClusterList {
	if in == nil {
		return nil
	}
	out := new(VectorClusterList)
	in.DeepCopyInto(out)
	return out
}

func (in *VectorClusterList) DeepCopyObject() runtime.Object { return in.DeepCopy() }
