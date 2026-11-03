package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Novirmor/homonto/internal/bitbucket"
	"github.com/spf13/cobra"
)

// hostCmd groups homonto-owned host transports for hosts without a
// first-party CLI that covers the h processes (ADR 0091).
func hostCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "host", Short: "Host API transports for the hosting workflows"}
	bb := &cobra.Command{Use: "bitbucket", Short: "Bitbucket Cloud REST transport (token from " + bitbucket.TokenEnv + ")"}
	bb.AddCommand(bitbucketAPICmd(), bitbucketAuthCmd())
	cmd.AddCommand(bb)
	return cmd
}

func bitbucketAPICmd() *cobra.Command {
	var method, input string
	var paginate bool
	cmd := &cobra.Command{
		Use:   "api ENDPOINT",
		Short: "Call the Bitbucket Cloud REST 2.0 API, like `gh api` (endpoint relative to " + bitbucket.API + ")",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := bitbucket.FromEnv(os.Getenv)
			if err != nil {
				return err
			}
			var body []byte
			switch input {
			case "":
			case "-":
				body, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20+1))
				if err != nil {
					return errors.New("bitbucket: reading --input failed")
				}
				if len(body) > 1<<20 {
					return errors.New("bitbucket: --input exceeds 1 MiB")
				}
				if !json.Valid(body) {
					return errors.New("bitbucket: --input must be a JSON document")
				}
			default:
				return errors.New("bitbucket: --input supports only - (stdin)")
			}
			out, err := client.Call(strings.ToUpper(method), args[0], body, paginate)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(append(out, '\n'))
			return err
		},
	}
	cmd.Flags().StringVarP(&method, "method", "X", "GET", "HTTP method: GET, POST, PUT or DELETE")
	cmd.Flags().StringVar(&input, "input", "", "request body: - reads a JSON document from stdin")
	cmd.Flags().BoolVar(&paginate, "paginate", false, "follow next links and print every page's values as one JSON array")
	return cmd
}

func bitbucketAuthCmd() *cobra.Command {
	auth := &cobra.Command{Use: "auth", Short: "Bitbucket Cloud credentials"}
	auth.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Check the configured Bitbucket API token without printing it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := bitbucket.FromEnv(os.Getenv)
			if err != nil {
				return err
			}
			out, err := client.Call("GET", "user", nil, false)
			if err != nil {
				return err
			}
			var user struct {
				Nickname  string `json:"nickname"`
				AccountID string `json:"account_id"`
			}
			if json.Unmarshal(out, &user) != nil || user.Nickname == "" {
				return errors.New("bitbucket: unexpected /user response")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Logged in to bitbucket.org as %s (%s)\n", user.Nickname, user.AccountID)
			return nil
		},
	})
	return auth
}
