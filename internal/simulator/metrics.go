package simulator

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	auc = promauto.NewCounter(prometheus.CounterOpts{
		Name: "application_uplink_count",
		Help: "The number of uplinks published by the application integration.",
	})
)

func applicationUplinkCounter() prometheus.Counter {
	return auc
}
