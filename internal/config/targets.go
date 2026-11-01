package config

import (
	"fmt"
	"strings"
)

// Target is one switch of a multi-target configuration. Anything left out is
// taken from the top level of the file.
type Target struct {
	// Name is an optional description, published by the service discovery.
	Name string `yaml:"name"`
	// Address is the switch host, and what Prometheus asks for in ?target=.
	Address         string    `yaml:"address"`
	Username        string    `yaml:"username"`
	Password        string    `yaml:"password"`
	PollRateSeconds *int      `yaml:"poll_rate_seconds"`
	TimeoutSeconds  *int      `yaml:"timeout_seconds"`
	PoE             *FlexBool `yaml:"poe"`
	STP             *bool     `yaml:"stp"`
	Firmware        string    `yaml:"firmware"`
}

// MultiTarget reports whether the file lists its switches under targets, which
// serves them on /probe instead of one switch on the metrics endpoint.
func (c Config) MultiTarget() bool {
	return len(c.Targets) > 0
}

// TargetConfig returns the configuration of target i: the top level of the file
// with the target's own settings over it. The result describes a single switch.
func (c Config) TargetConfig(i int) Config {
	t := c.Targets[i]
	out := c
	out.Targets = nil
	out.Web = WebConfig{}
	out.Address = normalizeAddress(strings.TrimSpace(t.Address))
	if t.Username != "" {
		out.Username = t.Username
	}
	if t.Password != "" {
		out.Password = t.Password
	}
	if t.PollRateSeconds != nil {
		out.PollRateSeconds = t.PollRateSeconds
	}
	if t.TimeoutSeconds != nil {
		out.TimeoutSeconds = t.TimeoutSeconds
	}
	if t.PoE != nil {
		out.PoE = *t.PoE
	}
	if t.STP != nil {
		out.STP = *t.STP
	}
	if t.Firmware != "" {
		out.Firmware = t.Firmware
	}
	return out
}

// validateTargets is Validate for a multi-target configuration: every target has
// to be valid as a switch by itself, and to be addressable.
func (c Config) validateTargets() error {
	if c.Address != "" {
		return fmt.Errorf("set either address or targets, not both (address may come from %s)", EnvAddress)
	}
	seen := make(map[string]struct{}, len(c.Targets))
	for i := range c.Targets {
		target := c.TargetConfig(i)
		if err := target.Validate(); err != nil {
			return fmt.Errorf("targets[%d]: %w", i, err)
		}
		if _, dup := seen[target.Address]; dup {
			return fmt.Errorf("targets[%d]: duplicate address %s", i, target.Address)
		}
		seen[target.Address] = struct{}{}
	}
	return c.Web.validate()
}
