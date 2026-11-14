package openbao

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	connectionLabel = "connection"
	methodLabel     = "method"
)

var (
	requestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "openbao_requests_total",
		Help: "HTTP requests sent to OpenBao, including retries.",
	}, []string{connectionLabel, methodLabel, "code"})

	requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "openbao_request_duration_seconds",
		Help:    "Duration of HTTP requests sent to OpenBao.",
		Buckets: prometheus.DefBuckets,
	}, []string{connectionLabel, methodLabel})

	loginsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "openbao_logins_total",
		Help: "Logins to OpenBao, by connection and result; steady state should add none.",
	}, []string{connectionLabel, "result"})

	tokenRenewalsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "openbao_token_renewals_total",
		Help: "Token renewals (renew-self), by connection and result.",
	}, []string{connectionLabel, "result"})
)

const (
	resultSuccess = "success"
	resultFailure = "failure"
)

func init() {
	metrics.Registry.MustRegister(requestsTotal, requestDuration, loginsTotal, tokenRenewalsTotal)
}

type instrumentedTransport struct {
	next       http.RoundTripper
	connection string
}

func (t *instrumentedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	method := req.Method
	if method == http.MethodGet && req.URL.Query().Get("list") == queryTrue {
		method = "LIST"
	}
	start := time.Now()
	resp, err := t.next.RoundTrip(req)
	requestDuration.WithLabelValues(t.connection, method).Observe(time.Since(start).Seconds())
	code := "error"
	if err == nil {
		code = strconv.Itoa(resp.StatusCode)
	}
	requestsTotal.WithLabelValues(t.connection, method, code).Inc()
	return resp, err
}

// CloseIdleConnections lets http.Client.CloseIdleConnections reach the wrapped transport.
func (t *instrumentedTransport) CloseIdleConnections() { closeIdle(t.next) }

func forgetConnectionMetrics(connection string) {
	requestsTotal.DeletePartialMatch(prometheus.Labels{connectionLabel: connection})
	requestDuration.DeletePartialMatch(prometheus.Labels{connectionLabel: connection})
	loginsTotal.DeletePartialMatch(prometheus.Labels{connectionLabel: connection})
	tokenRenewalsTotal.DeletePartialMatch(prometheus.Labels{connectionLabel: connection})
}
