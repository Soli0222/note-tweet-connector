package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds all application-specific metrics
type Metrics struct {
	// Webhook request metrics
	WebhookRequestsTotal   *prometheus.CounterVec
	WebhookRequestDuration *prometheus.HistogramVec
	WebhookRequestErrors   *prometheus.CounterVec

	// Content processing metrics
	Note2TweetTotal   prometheus.Counter
	Note2TweetSuccess prometheus.Counter
	Note2TweetErrors  prometheus.Counter
	Note2TweetSkipped *prometheus.CounterVec

	Tweet2NoteTotal   prometheus.Counter
	Tweet2NoteSuccess prometheus.Counter
	Tweet2NoteErrors  prometheus.Counter
	Tweet2NoteSkipped *prometheus.CounterVec

	// Tracker metrics
	TrackerEntriesTotal  prometheus.Gauge
	TrackerDuplicatesHit prometheus.Counter

	// Info metric
	BuildInfo *prometheus.GaugeVec
}

// New creates and registers all metrics to the default registry
func New(version string) *Metrics {
	return NewWithRegistry(version, prometheus.DefaultRegisterer)
}

// NewWithRegistry creates and registers all metrics to a custom registry
func NewWithRegistry(version string, registerer prometheus.Registerer) *Metrics {
	m := &Metrics{
		WebhookRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "webhook_requests_total",
				Help: "Total number of webhook requests received",
			},
			[]string{"source", "status"},
		),
		WebhookRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "webhook_request_duration_seconds",
				Help:    "Duration of webhook request processing",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"source"},
		),
		WebhookRequestErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "webhook_request_errors_total",
				Help: "Total number of webhook request errors",
			},
			[]string{"source", "error_type"},
		),

		Note2TweetTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "note2tweet_total",
				Help: "Total number of note to tweet conversions attempted",
			},
		),
		Note2TweetSuccess: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "note2tweet_success_total",
				Help: "Total number of successful note to tweet conversions",
			},
		),
		Note2TweetErrors: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "note2tweet_errors_total",
				Help: "Total number of failed note to tweet conversions",
			},
		),
		Note2TweetSkipped: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "note2tweet_skipped_total",
				Help: "Total number of skipped note to tweet conversions",
			},
			[]string{"reason"},
		),

		Tweet2NoteTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "tweet2note_total",
				Help: "Total number of tweet to note conversions attempted",
			},
		),
		Tweet2NoteSuccess: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "tweet2note_success_total",
				Help: "Total number of successful tweet to note conversions",
			},
		),
		Tweet2NoteErrors: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "tweet2note_errors_total",
				Help: "Total number of failed tweet to note conversions",
			},
		),
		Tweet2NoteSkipped: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "tweet2note_skipped_total",
				Help: "Total number of skipped tweet to note conversions",
			},
			[]string{"reason"},
		),

		TrackerEntriesTotal: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "tracker_entries_total",
				Help: "Current number of entries in the content tracker",
			},
		),
		TrackerDuplicatesHit: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "tracker_duplicates_hit_total",
				Help: "Total number of duplicate content detected",
			},
		),

		BuildInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "build_info",
				Help: "Build information",
			},
			[]string{"version"},
		),
	}

	// Register all metrics
	registerer.MustRegister(
		m.WebhookRequestsTotal,
		m.WebhookRequestDuration,
		m.WebhookRequestErrors,
		m.Note2TweetTotal,
		m.Note2TweetSuccess,
		m.Note2TweetErrors,
		m.Note2TweetSkipped,
		m.Tweet2NoteTotal,
		m.Tweet2NoteSuccess,
		m.Tweet2NoteErrors,
		m.Tweet2NoteSkipped,
		m.TrackerEntriesTotal,
		m.TrackerDuplicatesHit,
		m.BuildInfo,
	)

	// Set build info
	m.BuildInfo.WithLabelValues(version).Set(1)

	return m
}

// NewNoop creates metrics that are not registered (for testing)
func NewNoop() *Metrics {
	return &Metrics{
		WebhookRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "webhook_requests_total",
				Help: "Total number of webhook requests received",
			},
			[]string{"source", "status"},
		),
		WebhookRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "webhook_request_duration_seconds",
				Help:    "Duration of webhook request processing",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"source"},
		),
		WebhookRequestErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "webhook_request_errors_total",
				Help: "Total number of webhook request errors",
			},
			[]string{"source", "error_type"},
		),

		Note2TweetTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "note2tweet_total",
				Help: "Total number of note to tweet conversions attempted",
			},
		),
		Note2TweetSuccess: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "note2tweet_success_total",
				Help: "Total number of successful note to tweet conversions",
			},
		),
		Note2TweetErrors: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "note2tweet_errors_total",
				Help: "Total number of failed note to tweet conversions",
			},
		),
		Note2TweetSkipped: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "note2tweet_skipped_total",
				Help: "Total number of skipped note to tweet conversions",
			},
			[]string{"reason"},
		),

		Tweet2NoteTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "tweet2note_total",
				Help: "Total number of tweet to note conversions attempted",
			},
		),
		Tweet2NoteSuccess: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "tweet2note_success_total",
				Help: "Total number of successful tweet to note conversions",
			},
		),
		Tweet2NoteErrors: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "tweet2note_errors_total",
				Help: "Total number of failed tweet to note conversions",
			},
		),
		Tweet2NoteSkipped: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "tweet2note_skipped_total",
				Help: "Total number of skipped tweet to note conversions",
			},
			[]string{"reason"},
		),

		TrackerEntriesTotal: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "tracker_entries_total",
				Help: "Current number of entries in the content tracker",
			},
		),
		TrackerDuplicatesHit: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "tracker_duplicates_hit_total",
				Help: "Total number of duplicate content detected",
			},
		),

		BuildInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "build_info",
				Help: "Build information",
			},
			[]string{"version"},
		),
	}
}
