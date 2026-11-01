package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"cheap-switch-exporter/internal/fakeswitch"
)

// probe runs one scrape of target and returns the body.
func probe(t *testing.T, listenAddr, target string) string {
	t.Helper()
	status, body := get(t, "http://"+listenAddr+probePath+"?target="+url.QueryEscape(target), nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s?target=%s = %d\n%s", probePath, target, status, body)
	}
	return body
}

// startTwoSwitches runs the exporter against a healthy switch with spanning tree
// and one that refuses everything.
func startTwoSwitches(t *testing.T, extra string) (listenAddr, healthy, broken string, sw *fakeswitch.Switch) {
	t.Helper()
	clearEnv(t)
	healthy, sw = startFake(t, fakeswitch.Options{Profile: fakeswitch.ProfileSTP, Seed: 1})
	broken, _ = startFake(t, fakeswitch.Options{Profile: fakeswitch.ProfileUnauthorized})
	listenAddr = freeAddr(t)
	startExporter(t, writeConfig(t, fmt.Sprintf(`
username: admin
password: admin
poll_rate_seconds: 0
timeout_seconds: 2
%s
targets:
  - name: healthy
    address: %q
    stp: true
  - address: %q
`, extra, healthy, broken)), listenAddr)
	return listenAddr, healthy, broken, sw
}

func TestProbeServesEachTargetSeparately(t *testing.T) {
	listenAddr, healthy, broken, sw := startTwoSwitches(t, "")

	body := probe(t, listenAddr, healthy)
	if got := mustSample(t, body, "exporter_up"); got != 1 {
		t.Errorf("healthy exporter_up = %v, want 1", got)
	}
	for _, want := range []string{`port_state{port="1"} 1`, `port_rstp_state{port="1"} 3`} {
		if !strings.Contains(body, want) {
			t.Errorf("healthy: missing %q", want)
		}
	}

	body = probe(t, listenAddr, broken)
	if got := mustSample(t, body, "exporter_up"); got != 0 {
		t.Errorf("broken exporter_up = %v, want 0", got)
	}
	if strings.Contains(body, "port_state") || strings.Contains(body, "port_rstp_state") {
		t.Errorf("broken: the other switch's metrics leaked:\n%s", body)
	}

	// A probe of one switch does not poll the other, and stays per target.
	if c := sw.Counts(); c.PortStats != 1 || c.STPPorts != 1 {
		t.Errorf("healthy switch counts = %+v, want one poll", c)
	}
}

// The process metrics stay on /metrics, without polling any switch.
func TestProbeMetricsEndpointDoesNotPoll(t *testing.T) {
	listenAddr, _, _, sw := startTwoSwitches(t, "")

	body := scrape(t, listenAddr)
	if !strings.Contains(body, "go_goroutines") {
		t.Error("/metrics lost the process metrics")
	}
	if strings.Contains(body, "exporter_up") {
		t.Error("/metrics has switch metrics, with no switch to attribute them to")
	}
	if c := sw.Counts(); c.PortStats != 0 {
		t.Errorf("/metrics polled the switch: %+v", c)
	}
}

func TestProbeRejectsOtherTargets(t *testing.T) {
	listenAddr, _, _, _ := startTwoSwitches(t, "")

	for target, want := range map[string]int{
		"":              http.StatusBadRequest,
		"192.0.2.1":     http.StatusNotFound,
		"example.org/x": http.StatusNotFound,
	} {
		status, _ := get(t, "http://"+listenAddr+probePath+"?target="+url.QueryEscape(target), nil)
		if status != want {
			t.Errorf("target %q = %d, want %d", target, status, want)
		}
	}
}

func TestTargetsServesDiscovery(t *testing.T) {
	listenAddr, healthy, broken, _ := startTwoSwitches(t, "")

	status, body := get(t, "http://"+listenAddr+targetsPath, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s = %d", targetsPath, status)
	}
	var groups []targetGroup
	if err := json.Unmarshal([]byte(body), &groups); err != nil {
		t.Fatalf("not the service discovery format: %v\n%s", err, body)
	}
	if len(groups) != 2 ||
		groups[0].Targets[0] != healthy || groups[0].Labels["switch_name"] != "healthy" ||
		groups[1].Targets[0] != broken || groups[1].Labels == nil {
		t.Errorf("groups = %+v", groups)
	}
	if strings.Contains(body, "admin") {
		t.Errorf("the discovery shows credentials:\n%s", body)
	}
}

func TestProbeRoutesRequireAuth(t *testing.T) {
	listenAddr, healthy, _, sw := startTwoSwitches(t,
		"web:\n  auth_username: prometheus\n  auth_password: secret\n")

	for _, path := range []string{probePath + "?target=" + url.QueryEscape(healthy), targetsPath} {
		if status, _ := get(t, "http://"+listenAddr+path, nil); status != http.StatusUnauthorized {
			t.Errorf("%s without credentials = %d, want 401", path, status)
		}
	}
	if c := sw.Counts(); c.PortStats != 0 {
		t.Errorf("an unauthenticated probe reached the switch: %+v", c)
	}
}

func TestTelemetryPathMustNotShadowProbeRoutes(t *testing.T) {
	clearEnv(t)
	addr, _ := startFake(t, fakeswitch.Options{})
	path := writeConfig(t, fmt.Sprintf("username: admin\npassword: admin\ntargets:\n  - address: %q\n", addr))
	for _, telemetry := range []string{probePath, targetsPath} {
		err := run(t.Context(), []string{"-c", path, "--web.listen-address", freeAddr(t), "--web.telemetry-path", telemetry}, &strings.Builder{})
		if err == nil || !strings.Contains(err.Error(), "must not be") {
			t.Errorf("telemetry path %s: err = %v", telemetry, err)
		}
	}
}
