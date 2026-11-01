package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"cheap-switch-exporter/internal/config"
)

const (
	probePath   = "/probe"
	targetsPath = "/targets"
)

// targetGroup is one entry of the Prometheus HTTP service discovery format.
type targetGroup struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

// addProbeRoutes serves the switches of a multi-target configuration on top of
// srv's handler, which keeps the exporter's own metrics and the liveness probe.
//
// Each switch has one collector and one registry for the life of the process, so
// its session, its poll cache and its lock survive between scrapes: scrapes of
// one switch queue up, and scrapes of different switches do not wait for each
// other. Only addresses from the file can be probed, so the exporter cannot be
// pointed at another host, least of all with the credentials of a switch.
func addProbeRoutes(ctx context.Context, srv *http.Server, cfg config.Config, logger *slog.Logger) error {
	errLog := slog.NewLogLogger(logger.Handler(), slog.LevelError)

	handlers := make(map[string]http.Handler, len(cfg.Targets))
	groups := make([]targetGroup, 0, len(cfg.Targets))
	for i, t := range cfg.Targets {
		target := cfg.TargetConfig(i)
		registry := prometheus.NewRegistry()
		registry.MustRegister(newSwitchCollector(ctx, target, logger.With("target", target.Address)))

		budget := scrapeBudget(target)
		handlers[target.Address] = newMetricsHandler(registry, budget, errLog)
		if needed := budget + 5*time.Second; needed > srv.WriteTimeout {
			srv.WriteTimeout = needed
		}

		labels := map[string]string{}
		if t.Name != "" {
			labels["switch_name"] = t.Name
		}
		groups = append(groups, targetGroup{Targets: []string{target.Address}, Labels: labels})
	}
	discovery, err := json.Marshal(groups)
	if err != nil {
		return fmt.Errorf("encoding the service discovery: %w", err)
	}

	guard := func(h http.Handler) http.Handler {
		if cfg.AuthEnabled() {
			return basicAuth(h, cfg.Web.AuthUsername, cfg.Web.AuthPassword)
		}
		return h
	}

	mux := http.NewServeMux()
	mux.Handle(probePath, guard(probeHandler(handlers)))
	mux.Handle(targetsPath, guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(discovery)
	})))
	mux.Handle("/", srv.Handler)
	srv.Handler = mux
	return nil
}

func probeHandler(handlers map[string]http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.URL.Query().Get("target")
		if target == "" {
			http.Error(w, "the target parameter is missing", http.StatusBadRequest)
			return
		}
		handler, ok := handlers[target]
		if !ok {
			http.Error(w, fmt.Sprintf("target %q is not in the configuration", target), http.StatusNotFound)
			return
		}
		handler.ServeHTTP(w, r)
	})
}
