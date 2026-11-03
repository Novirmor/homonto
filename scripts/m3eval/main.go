// Command m3eval is the reproducible M3 evaluation (#59): decision behavior
// per provider and question type, and auto vs static routing on
// representative tasks. It spends inference only with --paid; without it, it
// validates the inputs and prints what a paid run would do.
//
//	go run ./scripts/m3eval decisions --config eval.toml [--providers a,b] [--paid] [--max-calls N]
//	go run ./scripts/m3eval routing --arms static=a.toml,auto=b.toml [--tasks DIR] [--reps N] [--paid]
//
// Results are JSON lines (--out) plus a Markdown summary on stdout.
package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/decision"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: m3eval decisions|routing [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "decisions":
		err = runDecisions(context.Background(), os.Args[2:], os.Stdout)
	case "routing":
		err = runRouting(context.Background(), os.Args[2:], os.Stdout)
	default:
		err = fmt.Errorf("unknown subcommand %q (decisions, routing)", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "m3eval:", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------- decisions

// Item is one labelled question. Expect is the right choice, "yes"/"no",
// or for a score the accepted [lo, hi] range on the request's scale.
type Item struct {
	ID      string           `json:"id"`
	Request decision.Request `json:"request"`
	Expect  json.RawMessage  `json:"expect"`
}

// DecisionResult is one item asked of one provider.
type DecisionResult struct {
	Provider string           `json:"provider"`
	Protocol string           `json:"protocol"`
	Item     string           `json:"item"`
	Kind     decision.Kind    `json:"kind"`
	Outcome  decision.Outcome `json:"outcome"`
	Correct  *bool            `json:"correct,omitempty"`
	// Brier is the squared error of P(yes) for yes-no items.
	Brier *float64 `json:"brier,omitempty"`
}

func runDecisions(ctx context.Context, args []string, stdout io.Writer) error {
	fl := flag.NewFlagSet("decisions", flag.ContinueOnError)
	cfgPath := fl.String("config", "", "homonto config with [decisions.providers] (required)")
	set := fl.String("set", "testdata/eval/m3/decisions.jsonl", "labelled question set")
	only := fl.String("providers", "", "comma-separated providers (default: all)")
	paid := fl.Bool("paid", false, "make the provider calls (costs money)")
	maxCalls := fl.Int("max-calls", 100, "refuse a paid run needing more calls than this")
	out := fl.String("out", "", "append JSON-line results here")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		return errors.New("decisions: --config is required")
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	items, err := loadItems(*set)
	if err != nil {
		return err
	}
	names := slices.Sorted(maps.Keys(cfg.Decisions.Providers))
	if *only != "" {
		names = nil
		seen := map[string]bool{}
		for _, raw := range strings.Split(*only, ",") {
			n := strings.TrimSpace(raw)
			if n == "" {
				return fmt.Errorf("decisions: --providers contains an empty provider name")
			}
			if seen[n] {
				return fmt.Errorf("decisions: --providers names provider %q more than once", n)
			}
			if _, ok := cfg.Decisions.Providers[n]; !ok {
				return fmt.Errorf("decisions: provider %q is not in %s", n, *cfgPath)
			}
			seen[n] = true
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return errors.New("decisions: the config declares no [decisions.providers]")
	}
	calls := len(names) * len(items)
	if !*paid {
		fmt.Fprintf(stdout, "dry run: %d items × %d providers (%s) = %d calls; rerun with --paid to ask them\n", len(items), len(names), strings.Join(names, ", "), calls)
		return nil
	}
	if calls > *maxCalls {
		return fmt.Errorf("decisions: %d calls exceed --max-calls %d", calls, *maxCalls)
	}
	var results []DecisionResult
	for _, name := range names {
		p := cfg.Decisions.Providers[name]
		proto, _ := decision.LookupProtocol(p.Protocol)
		key := os.Getenv(p.APIKeyEnv)
		if key == "" {
			return fmt.Errorf("decisions: provider %s: %s is not set", name, p.APIKeyEnv)
		}
		timeout := config.DefaultDecisionTimeout
		if cfg.Decisions.Timeout != "" {
			timeout, _ = time.ParseDuration(cfg.Decisions.Timeout)
		}
		provider := &decision.HTTPProvider{Name: name, Protocol: proto, Endpoint: cmp.Or(p.Endpoint, proto.DefaultEndpoint()), Model: p.Model, APIKey: key, Timeout: timeout}
		svc := decision.Service{RouteFor: func(string) (decision.Route, error) {
			return decision.Route{Provider: provider, MinConfidence: cfg.Decisions.MinConfidence}, nil
		}}
		for _, it := range items {
			o := svc.Decide(ctx, it.Request)
			r := DecisionResult{Provider: name, Protocol: p.Protocol, Item: it.ID, Kind: it.Request.Kind, Outcome: o}
			r.Correct, r.Brier = grade(it, o)
			results = append(results, r)
		}
	}
	if err := appendJSONL(*out, results); err != nil {
		return err
	}
	fmt.Fprint(stdout, summarizeDecisions(results))
	return nil
}

func loadItems(path string) ([]Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var items []Item
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "//") {
			continue
		}
		var it Item
		dec := json.NewDecoder(strings.NewReader(text))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&it); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if err := dec.Decode(&struct{}{}); err != io.EOF {
			return nil, fmt.Errorf("%s:%d: expected exactly one JSON value", path, line)
		}
		if err := it.Request.Validate(); err != nil {
			return nil, fmt.Errorf("%s:%d %s: %w", path, line, it.ID, err)
		}
		if _, _, err := expectation(it); err != nil {
			return nil, fmt.Errorf("%s:%d %s: %w", path, line, it.ID, err)
		}
		items = append(items, it)
	}
	return items, sc.Err()
}

// expectation decodes Expect for the item's kind.
func expectation(it Item) (string, [2]float64, error) {
	switch it.Request.Kind {
	case decision.Choice, decision.YesNo:
		var s string
		if err := json.Unmarshal(it.Expect, &s); err != nil {
			return "", [2]float64{}, fmt.Errorf("expect must be a string: %w", err)
		}
		if it.Request.Kind == decision.YesNo && s != "yes" && s != "no" {
			return "", [2]float64{}, errors.New(`a yes-no expect is "yes" or "no"`)
		}
		if it.Request.Kind == decision.Choice && !slices.Contains(it.Request.Options, s) {
			return "", [2]float64{}, fmt.Errorf("expect %q is not an option", s)
		}
		return s, [2]float64{}, nil
	default:
		var r []float64
		if it.Request.Kind != decision.Score || it.Request.Scale == nil || json.Unmarshal(it.Expect, &r) != nil || len(r) != 2 || r[0] > r[1] || r[0] < it.Request.Scale.Min || r[1] > it.Request.Scale.Max {
			return "", [2]float64{}, errors.New("a score expect is [lo, hi]")
		}
		return "", [2]float64{r[0], r[1]}, nil
	}
}

func grade(it Item, o decision.Outcome) (*bool, *float64) {
	if o.Result == nil {
		return nil, nil
	}
	want, span, _ := expectation(it)
	var ok bool
	var brier *float64
	switch it.Request.Kind {
	case decision.Choice:
		ok = o.Result.Choice == want
	case decision.YesNo:
		p := *o.Result.Probability
		truth := 0.0
		if want == "yes" {
			truth = 1
		}
		b := (p - truth) * (p - truth)
		brier = &b
		ok = (p >= 0.5) == (want == "yes")
	case decision.Score:
		ok = *o.Result.Score >= span[0] && *o.Result.Score <= span[1]
	}
	return &ok, brier
}

func summarizeDecisions(results []DecisionResult) string {
	type key struct {
		provider string
		kind     decision.Kind
	}
	groups := map[key][]DecisionResult{}
	for _, r := range results {
		k := key{r.Provider, r.Kind}
		groups[k] = append(groups[k], r)
	}
	keys := slices.SortedFunc(maps.Keys(groups), func(a, b key) int {
		return cmp.Or(strings.Compare(a.provider, b.provider), strings.Compare(string(a.kind), string(b.kind)))
	})
	var b strings.Builder
	b.WriteString("| Provider | Kind | n | Correct | Uncertain | Failed | Brier | p50 ms | Input tok | Cost USD |\n|---|---|---|---|---|---|---|---|---|---|\n")
	for _, k := range keys {
		rs := groups[k]
		var correct, graded, uncertain, failed, tokens int
		var brier []float64
		var cost float64
		var latencies []int64
		failures := map[string]int{}
		for _, r := range rs {
			latencies = append(latencies, r.Outcome.LatencyMS)
			if r.Correct != nil {
				graded++
				if *r.Correct {
					correct++
				}
			}
			if r.Brier != nil {
				brier = append(brier, *r.Brier)
			}
			switch r.Outcome.Status {
			case decision.StatusUncertain:
				uncertain++
			case decision.StatusFailed:
				failed++
				failures[r.Outcome.Failure]++
			}
			if u := r.Outcome.Usage; u != nil {
				tokens += u.InputTokens
				if u.CostUSD != nil {
					cost += *u.CostUSD
				}
			}
		}
		failedText := fmt.Sprint(failed)
		if failed > 0 {
			var parts []string
			for _, c := range slices.Sorted(maps.Keys(failures)) {
				parts = append(parts, fmt.Sprintf("%s %d", c, failures[c]))
			}
			failedText += " (" + strings.Join(parts, ", ") + ")"
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %s | %d | %s | %s | %d | %d | %s |\n", k.provider, k.kind, len(rs), ratio(correct, graded), uncertain, failedText, mean(brier), median(latencies), tokens, money(cost))
	}
	return b.String()
}

// ------------------------------------------------------------------ routing

// Task is one representative task: a fixture repository, a prompt for an
// agent and a command that verifies the result.
type Task struct {
	Prompt  string `toml:"prompt"`
	Agent   string `toml:"agent"`
	Verify  string `toml:"verify"`
	Timeout string `toml:"timeout"`
}

// RoutingResult is one task run under one arm.
type RoutingResult struct {
	Arm      string            `json:"arm"`
	Task     string            `json:"task"`
	Rep      int               `json:"rep"`
	Models   map[string]string `json:"models"` // agent → routed model
	Verified bool              `json:"verified"`
	Seconds  float64           `json:"seconds"`
	CostUSD  *float64          `json:"costUSD,omitempty"`
	Error    string            `json:"error,omitempty"`
	Log      string            `json:"log"` // the run directory, kept for audit
}

func runRouting(ctx context.Context, args []string, stdout io.Writer) error {
	fl := flag.NewFlagSet("routing", flag.ContinueOnError)
	tasksDir := fl.String("tasks", "testdata/eval/m3/tasks", "task directories (task.toml + repo/)")
	armsFlag := fl.String("arms", "", "name=config-fragment.toml,... (required); each fragment declares packages and [routing]")
	reps := fl.Int("reps", 1, "runs per task and arm")
	paid := fl.Bool("paid", false, "run OpenCode against real models (costs money)")
	homonto := fl.String("homonto", "homonto", "homonto binary")
	opencode := fl.String("opencode", "opencode", "opencode binary")
	keep := fl.String("work", "", "keep run directories here (default: a temp dir)")
	out := fl.String("out", "", "append JSON-line results here")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *reps <= 0 {
		return errors.New("routing: --reps must be positive")
	}
	arms, err := parseArms(*armsFlag)
	if err != nil {
		return err
	}
	tasks, err := loadTasks(*tasksDir)
	if err != nil {
		return err
	}
	if !*paid {
		fmt.Fprintf(stdout, "dry run: %d tasks × %d arms (%s) × %d reps = %d OpenCode runs; rerun with --paid\n",
			len(tasks), len(arms), strings.Join(slices.Sorted(maps.Keys(arms)), ", "), *reps, len(tasks)*len(arms)**reps)
		return nil
	}
	work := *keep
	if work == "" {
		if work, err = os.MkdirTemp("", "m3eval-"); err != nil {
			return err
		}
	}
	var results []RoutingResult
	for _, arm := range slices.Sorted(maps.Keys(arms)) {
		for _, name := range slices.Sorted(maps.Keys(tasks)) {
			for rep := 1; rep <= *reps; rep++ {
				dir := filepath.Join(work, fmt.Sprintf("%s-%s-%d", arm, name, rep))
				r := runTask(ctx, *homonto, *opencode, dir, filepath.Join(*tasksDir, name), tasks[name], arms[arm])
				r.Arm, r.Task, r.Rep = arm, name, rep
				results = append(results, r)
			}
		}
	}
	if err := appendJSONL(*out, results); err != nil {
		return err
	}
	fmt.Fprint(stdout, summarizeRouting(results))
	return nil
}

func parseArms(s string) (map[string]string, error) {
	arms := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		name, path, ok := strings.Cut(strings.TrimSpace(part), "=")
		name, path = strings.TrimSpace(name), strings.TrimSpace(path)
		if !ok || name == "" || path == "" {
			return nil, fmt.Errorf("routing: --arms wants name=fragment.toml,... (got %q)", s)
		}
		if _, exists := arms[name]; exists {
			return nil, fmt.Errorf("routing: --arms names arm %q more than once", name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		arms[name] = string(data)
	}
	return arms, nil
}

func loadTasks(dir string) (map[string]Task, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	tasks := map[string]Task{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name(), "task.toml"))
		if err != nil {
			return nil, err
		}
		var t Task
		if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&t); err != nil {
			return nil, fmt.Errorf("%s/task.toml: %w", e.Name(), err)
		}
		if t.Prompt == "" || t.Agent == "" || t.Verify == "" {
			return nil, fmt.Errorf("%s/task.toml: prompt, agent and verify are required", e.Name())
		}
		if t.Timeout != "" {
			d, err := time.ParseDuration(t.Timeout)
			if err != nil || d <= 0 {
				return nil, fmt.Errorf("%s/task.toml: timeout %q must be a positive duration", e.Name(), t.Timeout)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "repo")); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		tasks[e.Name()] = t
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("routing: no tasks in %s", dir)
	}
	return tasks, nil
}

func runTask(ctx context.Context, homonto, opencode, dir, taskDir string, t Task, fragment string) RoutingResult {
	r := RoutingResult{Log: dir, Models: map[string]string{}}
	fail := func(err error) RoutingResult { r.Error = err.Error(); return r }
	if err := copyTree(filepath.Join(taskDir, "repo"), dir); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "homonto.toml"), []byte(fragment), 0o644); err != nil {
		return fail(err)
	}
	sh := func(name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = dir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return out, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return out, nil
	}
	for _, step := range [][]string{{"git", "init", "-q"}, {"git", "add", "-A"}, {"git", "-c", "user.name=eval", "-c", "user.email=eval@localhost", "commit", "-qm", "fixture"}, {homonto, "apply", "--yes"}} {
		if _, err := sh(step[0], step[1:]...); err != nil {
			return fail(err)
		}
	}
	routes, err := sh(homonto, "explain", "routing", "--json")
	if err != nil {
		return fail(err)
	}
	var report struct {
		Routes []struct {
			Agent     string
			From      string
			Selection *struct{ Model string }
		}
	}
	if err := json.Unmarshal(routes, &report); err != nil {
		return fail(fmt.Errorf("explain routing: %w", err))
	}
	for _, rt := range report.Routes {
		if rt.Selection != nil {
			r.Models[rt.Agent] = rt.Selection.Model
		}
	}
	timeout, _ := time.ParseDuration(cmp.Or(t.Timeout, "20m"))
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(runCtx, opencode, "run", "--agent", t.Agent, "--format", "json", "--auto", t.Prompt)
	cmd.Dir = dir
	events, runErr := cmd.Output()
	r.Seconds = math.Round(time.Since(start).Seconds()*10) / 10
	_ = os.WriteFile(filepath.Join(dir, ".m3eval-events.jsonl"), events, 0o644)
	r.CostUSD = eventCost(events)
	if runErr != nil {
		r.Error = "opencode run: " + runErr.Error()
	}
	verify := exec.CommandContext(ctx, "sh", "-c", t.Verify)
	verify.Dir = dir
	vout, verr := verify.CombinedOutput()
	_ = os.WriteFile(filepath.Join(dir, ".m3eval-verify.log"), vout, 0o644)
	r.Verified = verr == nil
	return r
}

// eventCost sums the "cost" of every object that also reports "tokens" in
// OpenCode's JSON event stream. The stream format is OpenCode's, so this is
// best effort: nil when no event carries a cost.
func eventCost(events []byte) *float64 {
	var total float64
	found := false
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if c, ok := x["cost"].(float64); ok {
				if _, hasTokens := x["tokens"]; hasTokens {
					total += c
					found = true
					return
				}
			}
			for _, child := range x {
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(events))
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		var v any
		if json.Unmarshal(sc.Bytes(), &v) == nil {
			walk(v)
		}
	}
	if !found {
		return nil
	}
	return &total
}

func summarizeRouting(results []RoutingResult) string {
	type key struct{ arm, task string }
	groups := map[key][]RoutingResult{}
	for _, r := range results {
		groups[key{r.Arm, r.Task}] = append(groups[key{r.Arm, r.Task}], r)
	}
	keys := slices.SortedFunc(maps.Keys(groups), func(a, b key) int {
		return cmp.Or(strings.Compare(a.task, b.task), strings.Compare(a.arm, b.arm))
	})
	var b strings.Builder
	b.WriteString("| Task | Arm | Runs | Verified | Mean s | Cost USD | Models | Errors |\n|---|---|---|---|---|---|---|---|\n")
	for _, k := range keys {
		rs := groups[k]
		var verified, errs int
		var seconds []float64
		var cost float64
		costKnown := false
		models := map[string]bool{}
		for _, r := range rs {
			if r.Verified {
				verified++
			}
			if r.Error != "" {
				errs++
			}
			seconds = append(seconds, r.Seconds)
			if r.CostUSD != nil {
				cost += *r.CostUSD
				costKnown = true
			}
			for _, m := range r.Models {
				models[m] = true
			}
		}
		costText := "unknown"
		if costKnown {
			costText = money(cost)
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d/%d | %s | %s | %s | %d |\n", k.task, k.arm, len(rs), verified, len(rs), mean(seconds), costText, strings.Join(slices.Sorted(maps.Keys(models)), ", "), errs)
	}
	return b.String()
}

// ------------------------------------------------------------------ helpers

func appendJSONL[T any](path string, rows []T) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			f.Close()
			return err
		}
	}
	return f.Close()
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func ratio(n, d int) string {
	if d == 0 {
		return "—"
	}
	return fmt.Sprintf("%d/%d", n, d)
}

func mean(v []float64) string {
	if len(v) == 0 {
		return "—"
	}
	var s float64
	for _, x := range v {
		s += x
	}
	return fmt.Sprintf("%.3g", s/float64(len(v)))
}

func median(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Sorted(slices.Values(v))
	return s[len(s)/2]
}

func money(v float64) string { return fmt.Sprintf("%.6f", v) }
