package cli

import (
	"encoding/json"
	"fmt"

	"github.com/noviopenworks/homonto/internal/workspacemigration"
	"github.com/spf13/cobra"
)

func workspaceMigrateCmd() *cobra.Command {
	migrate := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate supported legacy workflow records to schema-2 ownership",
		Args:  cobra.NoArgs,
	}
	var manifest string
	var jsonMode bool
	plan := &cobra.Command{
		Use:   "plan",
		Short: "Inventory the legacy records migration without writing workspace state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !jsonMode {
				return fmt.Errorf("workspace migrate plan requires --json")
			}
			configPath, err := cmd.Flags().GetString("config")
			if err != nil {
				return err
			}
			result, err := workspacemigration.Build(configPath, manifest)
			if encodeErr := json.NewEncoder(cmd.OutOrStdout()).Encode(result); encodeErr != nil {
				return encodeErr
			}
			if err != nil {
				return fmt.Errorf("workspace migration plan blocked; inspect JSON output")
			}
			return nil
		},
	}
	plan.Flags().StringVar(&manifest, "manifest", "", "path to the explicit migration source manifest")
	plan.Flags().BoolVar(&jsonMode, "json", false, "emit the deterministic migration plan as JSON")
	_ = plan.MarkFlagRequired("manifest")

	var applyManifest, applyPlanHash string
	var applyYes bool
	apply := &cobra.Command{
		Use:   "apply",
		Short: "Apply a reviewed migration plan with a durable recovery journal",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !applyYes {
				return fmt.Errorf("workspace migrate apply requires --yes")
			}
			configPath, err := cmd.Flags().GetString("config")
			if err != nil {
				return err
			}
			result, err := workspacemigration.Apply(configPath, applyManifest, applyPlanHash)
			if err != nil {
				return err
			}
			cmd.Printf("%s\t%s\n", result.RunID, result.Status)
			return nil
		},
	}
	apply.Flags().StringVar(&applyManifest, "manifest", "", "path to the reviewed migration source manifest")
	apply.Flags().StringVar(&applyPlanHash, "plan-hash", "", "SHA-256 plan hash emitted by workspace migrate plan")
	apply.Flags().BoolVar(&applyYes, "yes", false, "confirm the reviewed migration without bypassing safety checks")
	_ = apply.MarkFlagRequired("manifest")
	_ = apply.MarkFlagRequired("plan-hash")

	var verifyRunID string
	var verifyJSON bool
	verify := &cobra.Command{
		Use:   "verify",
		Short: "Verify a completed migration receipt, records history, and bindings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !verifyJSON {
				return fmt.Errorf("workspace migrate verify requires --json")
			}
			configPath, err := cmd.Flags().GetString("config")
			if err != nil {
				return err
			}
			result, err := workspacemigration.Verify(configPath, verifyRunID)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		},
	}
	verify.Flags().StringVar(&verifyRunID, "run-id", "", "migration run ID from apply output")
	verify.Flags().BoolVar(&verifyJSON, "json", false, "emit content-free verification evidence as JSON")
	_ = verify.MarkFlagRequired("run-id")

	var recoverRunID, recoverAction, recoverPlanHash string
	var recoverYes bool
	recover := &cobra.Command{
		Use:   "recover",
		Short: "Resume or restore an interrupted migration journal",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !recoverYes {
				return fmt.Errorf("workspace migrate recover requires --yes")
			}
			configPath, err := cmd.Flags().GetString("config")
			if err != nil {
				return err
			}
			result, err := workspacemigration.Recover(configPath, recoverRunID, recoverAction, recoverPlanHash)
			if err != nil {
				return err
			}
			cmd.Printf("%s\t%s\n", result.RunID, result.Status)
			return nil
		},
	}
	recover.Flags().StringVar(&recoverRunID, "run-id", "", "interrupted migration run ID")
	recover.Flags().StringVar(&recoverAction, "action", "", "recovery action: resume or restore")
	recover.Flags().StringVar(&recoverPlanHash, "plan-hash", "", "reviewed SHA-256 hash emitted by workspace migrate plan")
	recover.Flags().BoolVar(&recoverYes, "yes", false, "confirm recovery without bypassing safety checks")
	_ = recover.MarkFlagRequired("run-id")
	_ = recover.MarkFlagRequired("action")
	_ = recover.MarkFlagRequired("plan-hash")

	migrate.AddCommand(plan, apply, verify, recover)
	return migrate
}
