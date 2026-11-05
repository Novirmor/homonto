package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/decision"
	"github.com/Novirmor/homonto/internal/fsutil"
	"github.com/spf13/cobra"
)

// Exit codes of `homonto decide` (ADR 0102). A plugin script branches on
// them without parsing JSON; the JSON outcome carries the detail.
const (
	decideUncertain = 10
	decideRefused   = 11
	decideFailed    = 12
)

func decideCmd() *cobra.Command {
	var (
		requestPath string
		task        string
		noRecord    bool
		format      string
		flagReq     decision.Request
		kind        string
		scaleMin    float64
		scaleMax    float64
		contexts    []string
		ctxFiles    []string
	)
	cmd := &cobra.Command{
		Use:   "decide",
		Short: "Ask the configured decision model a typed question (JSON in, JSON out)",
		Long: "Read one decision request (JSON: point, kind, question, options | scale+levels, context) " +
			"from stdin or --request, ask the provider [decisions] routes its point to, and print the " +
			"outcome as JSON. Exit 0 answered, 10 uncertain (below min_confidence; the result is still " +
			"printed), 11 refused, 12 any other provider failure. Homonto never substitutes an answer: " +
			"the calling plugin owns its fallback. The outcome is appended to .homonto/decisions.jsonl " +
			"without the question or context (docs/contracts/decisions.md).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			if format != "json" && format != "value" {
				return fmt.Errorf("decide: --format %q is not json or value", format)
			}
			var req decision.Request
			if flagReq.Point != "" {
				// A request built from flags, so a shell script needs no JSON
				// tooling to ask.
				if requestPath != "-" {
					return fmt.Errorf("decide: give the request as --request or as --point flags, not both")
				}
				req = flagReq
				req.Kind = decision.Kind(kind)
				if cmd.Flags().Changed("min") || cmd.Flags().Changed("max") {
					req.Scale = &decision.Scale{Min: scaleMin, Max: scaleMax}
				}
				if req.Context, err = decisionContext(contexts, ctxFiles); err != nil {
					return err
				}
			} else {
				in := cmd.InOrStdin()
				if requestPath != "-" {
					f, err := os.Open(requestPath)
					if err != nil {
						return err
					}
					defer f.Close()
					in = f
				}
				dec := json.NewDecoder(io.LimitReader(in, 1<<20))
				dec.DisallowUnknownFields()
				if err := dec.Decode(&req); err != nil {
					return fmt.Errorf("decide: read request: %w", err)
				}
				if err := dec.Decode(&struct{}{}); err != io.EOF {
					return fmt.Errorf("decide: request must contain exactly one JSON value")
				}
			}
			svc := decision.Service{RouteFor: func(point string) (decision.Route, error) {
				return decisionRoute(cfg, point)
			}}
			out := svc.Decide(cmd.Context(), req)
			out.Task = task
			if !noRecord {
				abs, err := filepath.Abs(cfgPath)
				if err != nil {
					return err
				}
				if err := decision.Append(filepath.Join(fsutil.RealDir(filepath.Dir(abs)), ".homonto"), out); err != nil {
					return fmt.Errorf("decide: record: %w", err)
				}
			}
			setExitCode(decideExitCode(out))
			if format == "value" {
				// The bare answer on stdout, the failure on stderr: the exit
				// code says which.
				if out.Result != nil {
					fmt.Fprintln(cmd.OutOrStdout(), resultValue(*out.Result))
				} else {
					cmd.PrintErrln("decide:", out.Error)
				}
				return nil
			}
			b, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return err
			}
			// Explicitly stdout: cobra's Println defaults to stderr, and a
			// script captures stdout.
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
	cmd.Flags().StringVar(&requestPath, "request", "-", "request JSON file, or - for stdin")
	cmd.Flags().StringVar(&task, "task", "", "workspace task this decision belongs to, kept in the record")
	cmd.Flags().BoolVar(&noRecord, "no-record", false, "do not append the outcome to .homonto/decisions.jsonl")
	cmd.Flags().StringVar(&format, "format", "json", "output: json (the outcome) or value (only the answer: choice, probability or score)")
	cmd.Flags().StringVar(&flagReq.Point, "point", "", "build the request from flags: decision point <package>.<point>")
	cmd.Flags().StringVar(&kind, "kind", "", "with --point: choice, yes-no or score")
	cmd.Flags().StringVar(&flagReq.Question, "question", "", "with --point: the question")
	cmd.Flags().StringArrayVar(&flagReq.Options, "option", nil, "with --point: a choice option (repeat)")
	cmd.Flags().StringArrayVar(&flagReq.Levels, "level", nil, "with --point: a score level, lowest first (repeat)")
	cmd.Flags().Float64Var(&scaleMin, "min", 0, "with --point: score scale minimum")
	cmd.Flags().Float64Var(&scaleMax, "max", 0, "with --point: score scale maximum")
	cmd.Flags().StringArrayVar(&contexts, "context", nil, "with --point: evidence KEY=VALUE (repeat)")
	cmd.Flags().StringArrayVar(&ctxFiles, "context-file", nil, "with --point: evidence KEY=PATH, read from the file (repeat)")
	return cmd
}

func decideExitCode(out decision.Outcome) int {
	switch {
	case out.Status == decision.StatusAnswered:
		return 0
	case out.Status == decision.StatusUncertain:
		return decideUncertain
	case out.Failure == decision.FailRefused:
		return decideRefused
	default:
		return decideFailed
	}
}

// decisionRoute builds the provider for a point from the config and the
// credential's environment variable.
func decisionRoute(cfg *config.Config, point string) (decision.Route, error) {
	r, err := cfg.DecisionRouteFor(point)
	if err != nil {
		return decision.Route{}, err
	}
	key := os.Getenv(r.Provider.APIKeyEnv)
	if key == "" {
		return decision.Route{}, fmt.Errorf("decision provider %s: environment variable %s is not set", r.ProviderName, r.Provider.APIKeyEnv)
	}
	proto, _ := decision.LookupProtocol(r.Provider.Protocol)
	return decision.Route{
		Provider:      &decision.HTTPProvider{Name: r.ProviderName, Protocol: proto, Endpoint: r.Endpoint, Model: r.Provider.Model, APIKey: key, Timeout: r.Timeout},
		MinConfidence: r.MinConfidence,
	}, nil
}

// decisionLines renders recorded outcomes for status.
func decisionLines(outcomes []decision.Outcome) []string {
	lines := make([]string, 0, len(outcomes))
	for _, o := range outcomes {
		line := fmt.Sprintf("decision: %s %s %s", o.At, o.Point, o.Status)
		if o.Result != nil {
			line += " " + describeResult(*o.Result)
		}
		if o.Failure != "" {
			line += " (" + o.Failure + ")"
		}
		if o.Provider != "" {
			line += " via " + o.Provider
		}
		if o.Task != "" {
			line += " task " + o.Task
		}
		lines = append(lines, line)
	}
	return lines
}

func describeResult(r decision.Result) string {
	var s string
	switch r.Kind {
	case decision.Choice:
		s = r.Choice
	case decision.YesNo:
		s = fmt.Sprintf("p(yes)=%.2f", *r.Probability)
	case decision.Score:
		s = fmt.Sprintf("score=%.2f", *r.Score)
	}
	if r.Confidence != nil {
		s += fmt.Sprintf(" confidence=%.2f", *r.Confidence)
	}
	return s
}

// decisionContext assembles evidence from KEY=VALUE and KEY=PATH flags.
func decisionContext(values, files []string) (map[string]string, error) {
	ctx := map[string]string{}
	for _, kv := range values {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("decide: --context %q must be KEY=VALUE", kv)
		}
		ctx[k] = v
	}
	for _, kp := range files {
		k, p, ok := strings.Cut(kp, "=")
		if !ok || k == "" || p == "" {
			return nil, fmt.Errorf("decide: --context-file %q must be KEY=PATH", kp)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("decide: --context-file %s: %w", k, err)
		}
		ctx[k] = string(data)
	}
	if len(ctx) == 0 {
		return nil, nil
	}
	return ctx, nil
}

// resultValue is the bare answer for --format value.
func resultValue(r decision.Result) string {
	switch r.Kind {
	case decision.YesNo:
		return strconv.FormatFloat(*r.Probability, 'f', -1, 64)
	case decision.Score:
		return strconv.FormatFloat(*r.Score, 'f', -1, 64)
	}
	return r.Choice
}
