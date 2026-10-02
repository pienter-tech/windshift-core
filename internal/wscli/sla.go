package wscli

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var slaWindowPattern = regexp.MustCompile(`^\d+[dhm]$`)

var slaCmd = &cobra.Command{
	Use:   "sla",
	Short: "Service-level agreement commands",
	Long:  `Read-only commands for viewing item SLA state and workspace compliance reports.`,
}

var slaItemCmd = &cobra.Command{
	Use:   "item <item-key-or-id>",
	Short: "Show the SLA state for an item",
	Long: `Show the read-only SLA state for a work item: ongoing and completed cycles,
elapsed and remaining time, breach and pause state, and calendar context.

Examples:
	ws sla item WI-123
	ws sla item 456`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		client, err := NewClient()
		if err != nil {
			return err
		}
		itemID, err := client.ResolveItemID(args[0])
		if err != nil {
			return fmt.Errorf("failed to resolve item: %w", err)
		}
		states, err := client.GetItemSLA(itemID)
		if err != nil {
			return fmt.Errorf("failed to get item SLA: %w", err)
		}
		output := NewOutput()
		output.Print(states)
		return nil
	},
}

var (
	slaReportFrom string
	slaReportTo   string

	slaAtRiskWithin  string
	slaAtRiskLimit   int
	slaBreachedLimit int
)

var slaReportCmd = &cobra.Command{
	Use:   "report [workspace-key]",
	Short: "Show the SLA compliance report for a workspace",
	Long: `Aggregate completed SLA cycles for a workspace. Uses the stored cycle
snapshots, so later configuration edits do not rewrite history.

Examples:
	ws sla report                # Uses the configured default workspace
	ws sla report PROJ --from 2025-01-01 --to 2025-04-01`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := NewClient()
		if err != nil {
			return err
		}
		wsKey := cfg.GetEffectiveWorkspace()
		if len(args) == 1 {
			wsKey = args[0]
		}
		if wsKey == "" {
			return fmt.Errorf("workspace is required: pass a workspace key or configure defaults.workspace_key")
		}
		workspaceID, err := client.ResolveWorkspaceID(wsKey)
		if err != nil {
			return fmt.Errorf("failed to resolve workspace: %w", err)
		}
		report, err := client.GetSLAReport(workspaceID, slaReportFrom, slaReportTo)
		if err != nil {
			return fmt.Errorf("failed to get SLA report: %w", err)
		}
		output := NewOutput()
		output.Print(report)
		return nil
	},
}

var slaAtRiskCmd = &cobra.Command{
	Use:   "at-risk",
	Short: "List items whose running SLA deadline is close or already past",
	Long: `List items in the selected workspace whose running SLA cycle has a deadline
within the given window. Items already marked breached have their deadline
cleared and are listed by "ws sla breached" instead.

Examples:
	ws sla at-risk -w PROJ                 # deadline within the next 2 days
	ws sla at-risk -w PROJ --within 12h
	ws sla at-risk -w PROJ --within 30m --limit 100`,
	RunE: func(_ *cobra.Command, _ []string) error {
		client, err := NewClient()
		if err != nil {
			return err
		}
		workspaceID, err := resolveRequiredWorkspace(client)
		if err != nil {
			return err
		}
		within, err := parseSLAWindow(slaAtRiskWithin)
		if err != nil {
			return err
		}
		query := fmt.Sprintf("slaRunning = true AND slaDeadline <= %s", within)
		items, err := client.ListSLAItems(query, &workspaceID, "sla_deadline", slaAtRiskLimit)
		if err != nil {
			return fmt.Errorf("failed to list at-risk items: %w", err)
		}
		output := NewOutput()
		output.Print(items)
		return nil
	},
}

var slaBreachedCmd = &cobra.Command{
	Use:   "breached",
	Short: "List items with a currently breached SLA cycle",
	Long: `List items in the selected workspace whose ongoing SLA cycle is breached.
Use "ws search --ql 'slaEverBreached = true'" to include historical breaches.

Examples:
	ws sla breached -w PROJ
	ws sla breached -w PROJ --limit 100`,
	RunE: func(_ *cobra.Command, _ []string) error {
		client, err := NewClient()
		if err != nil {
			return err
		}
		workspaceID, err := resolveRequiredWorkspace(client)
		if err != nil {
			return err
		}
		items, err := client.ListSLAItems("slaBreached = true", &workspaceID, "-updated_at", slaBreachedLimit)
		if err != nil {
			return fmt.Errorf("failed to list breached items: %w", err)
		}
		output := NewOutput()
		output.Print(items)
		return nil
	},
}

var slaMetricsFile string

var slaMetricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Manage SLA metric configuration",
	Long: `List, inspect, create, update, and delete workspace SLA metrics, including
their start/pause/stop conditions, ordered goals, and priority or fallback
targets.`,
}

var slaMetricsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the workspace's SLA metrics",
	Long: `List every SLA metric in the selected workspace with its conditions,
goals, and targets.

Examples:
	ws sla metrics list
	ws sla metrics list -w PROJ`,
	RunE: func(_ *cobra.Command, _ []string) error {
		client, err := NewClient()
		if err != nil {
			return err
		}
		workspaceID, err := resolveRequiredWorkspace(client)
		if err != nil {
			return err
		}
		metrics, err := client.ListSLAMetrics(workspaceID)
		if err != nil {
			return fmt.Errorf("failed to list SLA metrics: %w", err)
		}
		NewOutput().Print(metrics)
		return nil
	},
}

var slaMetricsGetCmd = &cobra.Command{
	Use:   "get <metric-id>",
	Short: "Show one SLA metric",
	Long: `Show one SLA metric by numeric ID, including its conditions, goals, and
targets.

Examples:
	ws sla metrics get 12
	ws sla metrics get 12 -w PROJ`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		metricID, err := strconv.Atoi(args[0])
		if err != nil || metricID <= 0 {
			return fmt.Errorf("metric-id must be a positive integer, got %q", args[0])
		}
		client, err := NewClient()
		if err != nil {
			return err
		}
		workspaceID, err := resolveRequiredWorkspace(client)
		if err != nil {
			return err
		}
		metric, err := client.GetSLAMetric(workspaceID, metricID)
		if err != nil {
			return fmt.Errorf("failed to get SLA metric: %w", err)
		}
		NewOutput().Print(metric)
		return nil
	},
}

var slaMetricsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an SLA metric from a JSON document",
	Long: `Create an SLA metric from a JSON document supplied with --file. The document
carries name, display_format, position, is_active, import_status, conditions,
and goals (each goal with targets).

Examples:
	ws sla metrics create --file metric.json
	ws sla metrics create --file metric.json -w PROJ`,
	RunE: func(_ *cobra.Command, _ []string) error {
		payload, err := loadSLAMetricFile(slaMetricsFile)
		if err != nil {
			return err
		}
		client, err := NewClient()
		if err != nil {
			return err
		}
		workspaceID, err := resolveRequiredWorkspace(client)
		if err != nil {
			return err
		}
		metric, err := client.CreateSLAMetric(workspaceID, payload)
		if err != nil {
			return fmt.Errorf("failed to create SLA metric: %w", err)
		}
		NewOutput().Print(metric)
		return nil
	},
}

var slaMetricsUpdateCmd = &cobra.Command{
	Use:   "update <metric-id>",
	Short: "Replace an SLA metric from a JSON document",
	Long: `Replace an SLA metric's configuration from a JSON document supplied with
--file. The document has the same shape as create; omitted fields reset to
their defaults.

Examples:
	ws sla metrics update 12 --file metric.json
	ws sla metrics update 12 --file metric.json -w PROJ`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		metricID, err := strconv.Atoi(args[0])
		if err != nil || metricID <= 0 {
			return fmt.Errorf("metric-id must be a positive integer, got %q", args[0])
		}
		payload, err := loadSLAMetricFile(slaMetricsFile)
		if err != nil {
			return err
		}
		client, err := NewClient()
		if err != nil {
			return err
		}
		workspaceID, err := resolveRequiredWorkspace(client)
		if err != nil {
			return err
		}
		metric, err := client.UpdateSLAMetric(workspaceID, metricID, payload)
		if err != nil {
			return fmt.Errorf("failed to update SLA metric: %w", err)
		}
		NewOutput().Print(metric)
		return nil
	},
}

var slaMetricsDeleteCmd = &cobra.Command{
	Use:   "delete <metric-id>",
	Short: "Delete an SLA metric",
	Long: `Delete an SLA metric. Its cycles and pending jobs are removed with it.

Examples:
	ws sla metrics delete 12
	ws sla metrics delete 12 -w PROJ`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		metricID, err := strconv.Atoi(args[0])
		if err != nil || metricID <= 0 {
			return fmt.Errorf("metric-id must be a positive integer, got %q", args[0])
		}
		client, err := NewClient()
		if err != nil {
			return err
		}
		workspaceID, err := resolveRequiredWorkspace(client)
		if err != nil {
			return err
		}
		if err := client.DeleteSLAMetric(workspaceID, metricID); err != nil {
			return fmt.Errorf("failed to delete SLA metric: %w", err)
		}
		NewOutput().Print(map[string]bool{"deleted": true})
		return nil
	},
}

// loadSLAMetricFile reads a metric document and rejects malformed JSON before
// it reaches the API.
func loadSLAMetricFile(path string) (json.RawMessage, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("--file is required")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // the path comes from the operator's own flag
	if err != nil {
		return nil, fmt.Errorf("read metric file: %w", err)
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("metric file %q is not valid JSON", path)
	}
	return json.RawMessage(raw), nil
}

// parseSLAWindow validates a positive QL relative literal such as 2d, 12h, or
// 30m. Keeping the value unquoted matters: QL treats a quoted string as text,
// not as a relative instant.
func parseSLAWindow(value string) (string, error) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if !slaWindowPattern.MatchString(trimmed) {
		return "", fmt.Errorf("invalid --within %q: use a whole number of days (d), hours (h), or minutes (m), e.g. 2d, 12h, 30m", value)
	}
	return trimmed, nil
}

func init() {
	rootCmd.AddCommand(slaCmd)
	slaCmd.AddCommand(slaItemCmd)
	slaCmd.AddCommand(slaReportCmd)
	slaCmd.AddCommand(slaAtRiskCmd)
	slaCmd.AddCommand(slaBreachedCmd)
	slaCmd.AddCommand(slaMetricsCmd)
	slaMetricsCmd.AddCommand(slaMetricsListCmd)
	slaMetricsCmd.AddCommand(slaMetricsGetCmd)
	slaMetricsCmd.AddCommand(slaMetricsCreateCmd)
	slaMetricsCmd.AddCommand(slaMetricsUpdateCmd)
	slaMetricsCmd.AddCommand(slaMetricsDeleteCmd)
	slaMetricsCreateCmd.Flags().StringVar(&slaMetricsFile, "file", "", "path to the metric JSON document (required)")
	_ = slaMetricsCreateCmd.MarkFlagRequired("file")
	slaMetricsUpdateCmd.Flags().StringVar(&slaMetricsFile, "file", "", "path to the metric JSON document (required)")
	_ = slaMetricsUpdateCmd.MarkFlagRequired("file")
	slaReportCmd.Flags().StringVar(&slaReportFrom, "from", "", "inclusive lower bound on the cycle stop time (RFC3339 or YYYY-MM-DD)")
	slaReportCmd.Flags().StringVar(&slaReportTo, "to", "", "exclusive upper bound on the cycle stop time (RFC3339 or YYYY-MM-DD)")
	slaAtRiskCmd.Flags().StringVar(&slaAtRiskWithin, "within", "2d", "deadline window: whole number of days (d), hours (h), or minutes (m), e.g. 2d, 12h, 30m")
	slaAtRiskCmd.Flags().IntVar(&slaAtRiskLimit, "limit", 50, "maximum items to return")
	slaBreachedCmd.Flags().IntVar(&slaBreachedLimit, "limit", 50, "maximum items to return")
}
