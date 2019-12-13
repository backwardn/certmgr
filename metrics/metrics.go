// Package metrics defines the Prometheus metrics in use.
package metrics

import (
	"fmt"
	"io"
	"net"
	"net/http"
	_ "net/http/pprof" // start a pprof endpoint
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

const metricsNamespace = "certmgr"

var (
	startTime time.Time

	// SpecWatchCount counts the number of specs being watched.
	SpecWatchCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Subsystem: "spec",
			Name:      "watched_total",
			Help:      "Number of specs being watched",
		},
		[]string{"spec_path", "svcmgr", "action", "ca"},
	)

	// SpecRefreshCount counts the number of PKI regeneration taken by a spec
	SpecRefreshCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "spec",
			Name:      "refresh_count",
			Help:      "Number of times a spec has determined PKI must be refreshed",
		},
		[]string{"spec_path"},
	)

	// SpecCheckCount counts the number of PKI regeneration taken by a spec
	SpecCheckCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "spec",
			Name:      "check_count",
			Help:      "Number of times a spec PKI was checked",
		},
		[]string{"spec_path"},
	)

	// SpecLoadCount counts the number of times a spec was loaded from disk
	SpecLoadCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "spec",
			Name:      "load_count",
			Help:      "Number of times a spec was loaded from disk",
		},
		[]string{"spec_path"},
	)

	// SpecLoadFailureCount counts the number of times a spec couldn't be loaded from disk
	SpecLoadFailureCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "spec",
			Name:      "load_failure_count",
			Help:      "Number of times a spec was loaded from disk but failed to be parsed",
		},
		[]string{"spec_path"},
	)
	// SpecExpires contains the time of the next certificate
	// expiry.
	SpecExpires = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Subsystem: "spec",
			Name:      "expire_timestamp",
			Help:      "The unix time for when the given spec and PKI type expires",
		},
		[]string{"spec_path", "type"},
	)

	// SpecExpiresBeforeThreshold exports how much lead time we give for trying to renew a cert
	// before it expires.
	SpecExpiresBeforeThreshold = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Subsystem: "spec",
			Name:      "expire_before_duration_seconds",
			Help:      "When a spec is within this number of seconds of an expiry, renewal begins",
		},
		[]string{"spec_path"},
	)

	// SpecWriteCount contains the number of times the PKI on disk was written
	SpecWriteCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "spec",
			Name:      "write_count",
			Help:      "The number of times PKI on disk has been rewritten",
		},
		[]string{"spec_path"},
	)

	// SpecWriteFailureCount contains the number of times the PKI on disk failed to be written
	SpecWriteFailureCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "spec",
			Name:      "write_failure_count",
			Help:      "The number of times PKI on disk failed to be rewritten",
		},
		[]string{"spec_path"},
	)

	// SpecRequestFailureCount counts the number of times a spec failed to request a certificate from upstream.
	SpecRequestFailureCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "spec",
			Name:      "request_failure_count",
			Help:      "Number of failed requests to CA authority for new PKI material",
		},
		[]string{"spec_path"},
	)

	// SpecInterval is set to the interval at which a cert manager wakes up and does its checks
	SpecInterval = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Subsystem: "spec",
			Name:      "interval_seconds",
			Help:      "the time interval that this spec will sleep for between checks",
		},
		[]string{"spec_path"},
	)

	// ActionAttemptedCount counts actions taken by a spec
	ActionAttemptedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "action_attempted_count",
			Help:      "Number of times a spec has taken action",
		},
		[]string{"spec_path", "change_type"},
	)

	// ActionFailedCount counts failed actions taken by a spec
	ActionFailedCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "action_failed_count",
			Help:      "Number of failed action runs for a spec",
		},
		[]string{"spec_path", "change_type"},
	)
)

func init() {
	startTime = time.Now()

	prometheus.MustRegister(SpecWatchCount)
	prometheus.MustRegister(SpecRefreshCount)
	prometheus.MustRegister(SpecCheckCount)
	prometheus.MustRegister(SpecLoadCount)
	prometheus.MustRegister(SpecLoadFailureCount)
	prometheus.MustRegister(SpecExpires)
	prometheus.MustRegister(SpecExpiresBeforeThreshold)
	prometheus.MustRegister(SpecWriteCount)
	prometheus.MustRegister(SpecWriteFailureCount)
	prometheus.MustRegister(SpecRequestFailureCount)
	prometheus.MustRegister(SpecInterval)
	prometheus.MustRegister(ActionAttemptedCount)
	prometheus.MustRegister(ActionFailedCount)
}

var indexPage = `<html>
<head><title>Certificate Manager</title></head>
  <body>
    <h2>Certificate Manager</h2>
    <p>Server started at %s, listening on %s</p>
    <p><a href="https://github.com/cloudflare/certmgr/">GitHub</a></p>
    <h4>Endpoints</h4>
    <ul>
      <li>Prometheus endpoint: <a href="/metrics"><code>/metrics</code></a></li>
      <li>pprof endpoint: <a href="/debug/pprof"><code>/debug/pprof</code></a></li>
    </ul>
  </body>
</html>
`

func genServeIndex(addr string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		page := fmt.Sprintf(indexPage, startTime.Format("2006-01-02T15:04:05-0700"), addr)
		io.WriteString(w, page)
	}
}

// Start initialises the Prometheus endpoint if metrics have been
// configured.
func Start(addr, port string) {
	if addr == "" || port == "" {
		log.Warning("metrics: no prometheus address or port configured")
		return
	}

	addr = net.JoinHostPort(addr, port)
	http.HandleFunc("/", genServeIndex(addr))
	http.Handle("/metrics", promhttp.Handler())

	log.Infof("metrics: starting Prometheus endpoint on http://%s/", addr)
	go func() {
		log.Fatal(http.ListenAndServe(addr, nil))
	}()
}
