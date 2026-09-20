package cmd

import (
	"fmt"

	"github.com/abhi-vmlinuz/nexus-framework/nexus-cli/client"
	"github.com/spf13/cobra"
)

// newTelemetryCmd implements: nexus telemetry export --format csv --from ... --to ... --type ... --out report.csv
func newTelemetryCmd(makeClient func() *client.Client) *cobra.Command {
	var format, from, to, typ, out string
	cmd := &cobra.Command{
		Use:   "telemetry",
		Short: "Telemetry export (JSON/CSV event log for pitch report)",
	}
	exp := &cobra.Command{
		Use:   "export",
		Short: "Export telemetry events to JSON or CSV",
		Example: `  nexus telemetry export --format csv --out comp-report.csv
  nexus telemetry export --format json --type session_created --out sessions.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				return fmt.Errorf("--out is required")
			}
			if format != "json" && format != "csv" {
				return fmt.Errorf("--format must be json or csv")
			}
			c := makeClient()
			if err := c.ExportTelemetry(format, from, to, typ, out); err != nil {
				return err
			}
			fmt.Printf("Telemetry exported: %s (%s)\n", out, format)
			return nil
		},
	}
	exp.Flags().StringVar(&format, "format", "csv", "json or csv")
	exp.Flags().StringVar(&from, "from", "", "RFC3339 start filter")
	exp.Flags().StringVar(&to, "to", "", "RFC3339 end filter")
	exp.Flags().StringVar(&typ, "type", "", "event type filter")
	exp.Flags().StringVar(&out, "out", "", "output file path")
	cmd.AddCommand(exp)
	return cmd
}
