module github.com/manojrevenkar12-web/vector-db-lifecycle-operator/operator

go 1.24

require (
	github.com/manojrevenkar12-web/vector-db-lifecycle-operator v0.0.0
	k8s.io/api v0.32.3
	k8s.io/apimachinery v0.32.3
	k8s.io/client-go v0.32.3
	sigs.k8s.io/controller-runtime v0.20.4
)

replace github.com/manojrevenkar12-web/vector-db-lifecycle-operator => ../
