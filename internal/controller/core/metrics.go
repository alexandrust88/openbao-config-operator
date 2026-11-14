package core

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var connectionReady = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "openbao_connection_ready",
	Help: "1 when the connection is Ready, 0 otherwise.",
}, []string{"kind", "namespace", "name"})

func init() {
	metrics.Registry.MustRegister(connectionReady)
}
