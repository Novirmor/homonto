// Package routing turns a routing policy into a concrete execution model
// (ADR 0099, ADR 0100). A static policy is its model. An auto policy selects
// from the models the harness reports, filtered by the policy's constraints
// and ordered by its preference, and falls back to its declared fallback
// when discovery fails or nothing qualifies. Selection is deterministic for a
// given config and discovered catalog, and every selection carries its
// reason so plan, explain and status can show it.
package routing

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Novirmor/homonto/internal/config"
)

// Discoverer lists the execution models the harness can run, as the
// harness's own model ids.
type Discoverer interface {
	Models(ctx context.Context) ([]string, error)
}

// Static is a fixed model list, for a harness that cannot list its models:
// the ids the config describes. An empty list is a discovery failure, so an
// auto policy falls back.
type Static []string

// Models returns the list.
func (s Static) Models(context.Context) ([]string, error) {
	if len(s) == 0 {
		return nil, errors.New("this harness cannot list its models; describe candidates under [routing.models.<harness>]")
	}
	return append([]string(nil), s...), nil
}

// Selection is the resolved model for one policy.
type Selection struct {
	Model    string `json:"model"`
	Variant  string `json:"variant,omitempty"`
	Mode     string `json:"mode"`               // static | auto
	Fallback bool   `json:"fallback,omitempty"` // auto chose its fallback
	Reason   string `json:"reason"`
}

// Discovery states, for records.
const (
	DiscoveryNotRun = "not-run"
	DiscoveryOK     = "ok"
	DiscoveryFailed = "failed"
)

// Resolver resolves policies against one harness. Discovery runs at most
// once per Resolver, and only when an auto policy is resolved: a config with
// only static policies never calls it.
type Resolver struct {
	Discover Discoverer
	// Models is what the config states about each model id.
	Models map[string]config.ModelInfo

	mu        sync.Mutex
	ran       bool
	available []string
	err       error
}

// DiscoveryState reports whether discovery ran and how it went.
func (r *Resolver) DiscoveryState() (state string, models int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case !r.ran:
		return DiscoveryNotRun, 0, nil
	case r.err != nil:
		return DiscoveryFailed, 0, r.err
	}
	return DiscoveryOK, len(r.available), nil
}

func (r *Resolver) discover(ctx context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ran {
		return r.available, r.err
	}
	r.ran = true
	if r.Discover == nil {
		r.err = errors.New("no model discovery for this harness")
		return nil, r.err
	}
	r.available, r.err = r.Discover.Models(ctx)
	if r.err == nil && len(r.available) == 0 {
		r.err = errors.New("the harness reported no models")
	}
	return r.available, r.err
}

// Resolve returns the model policy p selects. label names the policy's
// config key in errors.
func (r *Resolver) Resolve(ctx context.Context, label string, p config.RoutePolicy) (Selection, error) {
	if p.ModeOrDefault() == config.RouteStatic {
		return Selection{Model: p.Model, Variant: p.Variant, Mode: config.RouteStatic, Reason: "static"}, nil
	}
	available, err := r.discover(ctx)
	if err != nil {
		return Selection{Model: p.Fallback, Mode: config.RouteAuto, Fallback: true,
			Reason: "fallback: model discovery failed (" + oneLine(err.Error()) + ")"}, nil
	}
	pool := available
	if len(p.Candidates) > 0 {
		pool = nil
		for _, id := range p.Candidates {
			if slices.Contains(available, id) {
				pool = append(pool, id)
			}
		}
	}
	type scored struct {
		id    string
		order int
		info  config.ModelInfo
	}
	var ok []scored
	for i, id := range pool {
		info := r.Models[id]
		if !r.admits(p, id, info) {
			continue
		}
		ok = append(ok, scored{id, i, info})
	}
	if len(ok) == 0 {
		if !slices.Contains(available, p.Fallback) {
			return Selection{}, fmt.Errorf("routing: %s: no available model satisfies the policy, and its fallback %q is not among the %d models the harness reports", label, p.Fallback, len(available))
		}
		return Selection{Model: p.Fallback, Mode: config.RouteAuto, Fallback: true,
			Reason: fmt.Sprintf("fallback: none of %d available models satisfies the policy", len(pool))}, nil
	}
	metric := func(s scored) *float64 { return nil }
	switch p.PreferOrDefault() {
	case config.PreferCost:
		metric = func(s scored) *float64 { return s.info.Cost }
	case config.PreferLatency:
		metric = func(s scored) *float64 { return s.info.Latency }
	}
	slices.SortStableFunc(ok, func(a, b scored) int {
		ma, mb := metric(a), metric(b)
		switch {
		case ma != nil && mb != nil && *ma != *mb:
			return cmp.Compare(*ma, *mb)
		case ma != nil && mb == nil:
			return -1
		case ma == nil && mb != nil:
			return 1
		}
		if len(p.Candidates) > 0 {
			return cmp.Compare(a.order, b.order)
		}
		return strings.Compare(a.id, b.id)
	})
	return Selection{Model: ok[0].id, Mode: config.RouteAuto,
		Reason: fmt.Sprintf("auto: %d of %d available models qualify; preferred by %s", len(ok), len(pool), p.PreferOrDefault())}, nil
}

// admits applies the policy's constraints. A constraint on a property the
// config does not state for the model excludes it: nothing is guessed.
func (r *Resolver) admits(p config.RoutePolicy, id string, info config.ModelInfo) bool {
	if len(p.Providers) > 0 {
		provider, _, _ := strings.Cut(id, "/")
		if !slices.Contains(p.Providers, provider) {
			return false
		}
	}
	for _, need := range p.Needs {
		if !slices.Contains(info.Tags, need) {
			return false
		}
	}
	if p.MaxCost != nil && (info.Cost == nil || *info.Cost > *p.MaxCost) {
		return false
	}
	if p.MaxLatency != nil && (info.Latency == nil || *info.Latency > *p.MaxLatency) {
		return false
	}
	return true
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// OpenCodeCLI discovers models with `opencode models`, which prints one
// provider/model id per line for every provider OpenCode can use.
type OpenCodeCLI struct {
	Binary  string // default "opencode"
	Dir     string // working directory (the project root)
	Timeout time.Duration
}

var modelLine = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@:-]*/[^\s]+$`)

// Models runs the CLI and parses its output.
func (o OpenCodeCLI) Models(ctx context.Context) ([]string, error) {
	bin := cmp.Or(o.Binary, "opencode")
	timeout := cmp.Or(o.Timeout, 60*time.Second)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "models")
	cmd.Dir = o.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s models timed out after %s", bin, timeout)
		}
		return nil, fmt.Errorf("%s models: %v: %s", bin, err, oneLine(stderr.String()))
	}
	return ParseModelList(stdout.Bytes()), nil
}

// ParseModelList extracts provider/model ids, one per line, ignoring
// anything else (banners, blank lines), deduplicated and sorted.
func ParseModelList(out []byte) []string {
	var ids []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if modelLine.MatchString(line) {
			ids = append(ids, line)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}
