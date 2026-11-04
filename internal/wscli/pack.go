package wscli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// Framework pack commands (WI-1336, WI-1141): a pack binds a configuration set
// (schema), a workspace content bundle, and plugin references. Built-in packs
// ship inside the server and are addressed by name; external packs are supplied
// as a gzipped tar archive. All three commands go over the same /packs API the
// UI uses.

var packCmd = &cobra.Command{
	Use:   "pack",
	Short: "List, apply, and verify framework packs",
	Long: `List, apply, or verify framework packs: versioned units binding a
configuration set (schema), a workspace content bundle, and plugin references.

Built-in packs ship with the server and are listed by name; external packs are
supplied as a gzipped tar archive.`,
}

type builtinPackSummary struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	HasContent  bool     `json:"has_content"`
	Plugins     []string `json:"plugins"`
}

var packListCmd = &cobra.Command{
	Use:   "ls",
	Short: "List built-in framework packs",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		client, err := NewClient()
		if err != nil {
			return err
		}
		var doc struct {
			Data []builtinPackSummary `json:"data"`
		}
		if err := client.GET("/rest/api/v2/packs", &doc); err != nil {
			return fmt.Errorf("pack list failed: %w", err)
		}
		if outputFormat == "" || outputFormat == "table" {
			if len(doc.Data) == 0 {
				_, _ = fmt.Fprintln(stdout, "No built-in packs.")
				return nil
			}
			for _, p := range doc.Data {
				notes := ""
				if p.HasContent {
					notes += " +content"
				}
				if len(p.Plugins) > 0 {
					notes += " plugins=" + strings.Join(p.Plugins, ",")
				}
				_, _ = fmt.Fprintf(stdout, "%-16s %-10s %s%s\n", p.Name, p.Version, p.Description, notes)
			}
			return nil
		}
		NewOutput().Print(doc.Data)
		return nil
	},
}

var packApplyCmd = &cobra.Command{
	Use:   "apply [pack.tar.gz]",
	Short: "Provision a new workspace from a framework pack",
	Args:  cobra.MaximumNArgs(1),
	Long: `Provision a new workspace from a framework pack. The pack's schema,
content, and conformance are applied to the workspace created by
--workspace-name. Framework packs only apply to new workspaces; an existing
workspace name is refused.

Use --builtin <name> to apply a pack that ships with the server; otherwise pass
an archive path.

Examples:
  ws pack apply --builtin helpdesk --workspace-name "Helpdesk"
  ws pack apply iso-27001-1.0.0.tar.gz --workspace-name "ISO"`,
	RunE: func(_ *cobra.Command, args []string) error {
		if packBuiltinName != "" {
			if len(args) != 0 {
				return fmt.Errorf("--builtin does not take an archive path")
			}
			return runBuiltinPack("apply", packBuiltinName)
		}
		if len(args) != 1 {
			return fmt.Errorf("exactly one pack archive path is required unless --builtin is set")
		}
		data, err := os.ReadFile(args[0]) //nolint:gosec // the path comes from the operator's own flag
		if err != nil {
			return fmt.Errorf("read pack archive: %w", err)
		}
		fields := packTargetFields()
		client, err := NewClient()
		if err != nil {
			return err
		}
		var doc struct {
			Data json.RawMessage `json:"data"`
		}
		if err := client.Upload(http.MethodPost, "/rest/api/v2/packs/apply", "file", args[0], data, fields, &doc); err != nil {
			return fmt.Errorf("pack apply failed: %w", err)
		}
		return printConformanceJSON(doc.Data)
	},
}

var packVerifyCmd = &cobra.Command{
	Use:   "verify [pack.tar.gz]",
	Short: "Validate a framework pack without applying it",
	Args:  cobra.MaximumNArgs(1),
	Long: `Check the pack's manifest, referenced files, and plugin requirements
against the connected instance, and print the apply report a real apply would
start from.

Use --builtin <name> to verify a pack that ships with the server; otherwise pass
an archive path.

Examples:
  ws pack verify --builtin helpdesk --workspace-name "Helpdesk"
  ws pack verify iso-27001-1.0.0.tar.gz --workspace-name "ISO"`,
	RunE: func(_ *cobra.Command, args []string) error {
		if packBuiltinName != "" {
			if len(args) != 0 {
				return fmt.Errorf("--builtin does not take an archive path")
			}
			return runBuiltinPack("verify", packBuiltinName)
		}
		if len(args) != 1 {
			return fmt.Errorf("exactly one pack archive path is required unless --builtin is set")
		}
		data, err := os.ReadFile(args[0]) //nolint:gosec // the path comes from the operator's own flag
		if err != nil {
			return fmt.Errorf("read pack archive: %w", err)
		}
		fields := packTargetFields()
		client, err := NewClient()
		if err != nil {
			return err
		}
		var doc struct {
			Data json.RawMessage `json:"data"`
		}
		if err := client.Upload(http.MethodPost, "/rest/api/v2/packs/verify", "file", args[0], data, fields, &doc); err != nil {
			return fmt.Errorf("pack verify failed: %w", err)
		}
		return printConformanceJSON(doc.Data)
	},
}

// runBuiltinPack applies or verifies a server-embedded pack by name against the
// new workspace named by --workspace-name.
func runBuiltinPack(action, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("--builtin requires a pack name")
	}
	workspaceName := strings.TrimSpace(packWorkspaceName)
	if workspaceName == "" {
		return fmt.Errorf("--workspace-name is required")
	}
	body := map[string]any{"workspace_name": workspaceName}
	client, err := NewClient()
	if err != nil {
		return err
	}
	var doc struct {
		Data json.RawMessage `json:"data"`
	}
	endpoint := "/rest/api/v2/packs/" + url.PathEscape(name) + "/" + action
	if err := client.POST(endpoint, body, &doc); err != nil {
		return fmt.Errorf("pack %s failed: %w", action, err)
	}
	return printConformanceJSON(doc.Data)
}

// packTargetFields renders the workspace selector shared by the archive upload
// endpoints as multipart fields.
func packTargetFields() map[string]string {
	fields := map[string]string{}
	if name := strings.TrimSpace(packWorkspaceName); name != "" {
		fields["workspace_name"] = name
	}
	return fields
}

var (
	packWorkspaceName string
	packBuiltinName   string
)

func init() {
	rootCmd.AddCommand(packCmd)
	packCmd.AddCommand(packListCmd)
	packCmd.AddCommand(packApplyCmd)
	packCmd.AddCommand(packVerifyCmd)

	packApplyCmd.Flags().StringVar(&packBuiltinName, "builtin", "", "apply a server-built-in pack by name instead of an archive")
	packApplyCmd.Flags().StringVar(&packWorkspaceName, "workspace-name", "", "name of the new workspace to provision")
	_ = packApplyCmd.MarkFlagRequired("workspace-name")
	packVerifyCmd.Flags().StringVar(&packBuiltinName, "builtin", "", "verify a server-built-in pack by name instead of an archive")
	packVerifyCmd.Flags().StringVar(&packWorkspaceName, "workspace-name", "", "name of the new workspace to verify against")
	_ = packVerifyCmd.MarkFlagRequired("workspace-name")
}
