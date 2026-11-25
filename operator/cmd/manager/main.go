// Command manager runs the VectorCluster operator.
package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	v1 "github.com/manojrevenkar12-web/vector-db-lifecycle-operator/operator/api/v1alpha1"
	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/operator/internal/controller"
)

func main() {
	var metrics, probes string
	var leader bool
	flag.StringVar(&metrics, "metrics-bind-address", ":8080", "metrics endpoint")
	flag.StringVar(&probes, "health-probe-bind-address", ":8081", "health and readiness endpoint")
	flag.BoolVar(&leader, "leader-elect", false, "elect one active manager")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("setup")

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		log.Error(err, "scheme")
		os.Exit(1)
	}
	if err := v1.AddToScheme(scheme); err != nil {
		log.Error(err, "scheme")
		os.Exit(1)
	}
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metrics},
		HealthProbeBindAddress: probes,
		LeaderElection:         leader,
		LeaderElectionID:       "vector-db-lifecycle-operator.lifecycle.vectordb.dev",
	})
	if err != nil {
		log.Error(err, "manager")
		os.Exit(1)
	}
	if err := (&controller.Reconciler{Client: mgr.GetClient()}).SetupWithManager(mgr); err != nil {
		log.Error(err, "controller")
		os.Exit(1)
	}
	_ = mgr.AddHealthzCheck("healthz", healthz.Ping)
	_ = mgr.AddReadyzCheck("readyz", healthz.Ping)
	log.Info("starting")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "run")
		os.Exit(1)
	}
}
