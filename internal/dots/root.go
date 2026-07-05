package dots

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// App holds process-wide CLI settings shared by dots commands.
type App struct {
	configPath string
	profile    string
	version    string
	out        io.Writer
	errOut     io.Writer
}

// NewRootCommand builds the root dots command tree.
func NewRootCommand(version string) *cobra.Command {
	app := &App{
		version: version,
		out:     os.Stdout,
		errOut:  os.Stderr,
	}

	root := &cobra.Command{
		Use:           "dots",
		Short:         "Copy-based dotfiles manager",
		Long:          "dots is a minimal copy-based dotfiles manager with explicit profiles and local apply state.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(app.out)
	root.SetErr(app.errOut)
	root.PersistentFlags().StringVar(&app.configPath, "config", "", "config file path (overrides DOTS_CONFIG; default $XDG_CONFIG_HOME/dots/config.toml or ~/.config/dots/config.toml)")
	root.PersistentFlags().StringVar(&app.profile, "profile", "", "profile name (overrides DOTS_PROFILE and default_profile)")

	root.AddCommand(app.newInitCommand())
	root.AddCommand(app.newAddCommand())
	root.AddCommand(app.newApplyCommand())
	root.AddCommand(app.newSyncCommand())
	root.AddCommand(app.newDiffCommand())
	root.AddCommand(app.newStatusCommand())
	root.AddCommand(app.newDoctorCommand())
	root.AddCommand(app.newListCommand())
	root.AddCommand(app.newReindexCommand())
	root.AddCommand(app.newForgetCommand())
	root.AddCommand(newCompletionCommand(root))
	root.AddCommand(app.newManCommand(root))

	return root
}

func newCompletionCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long:  "Generate shell completion scripts. Source the output from your shell startup file or install it in the appropriate completions directory.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return root.GenBashCompletionV2(cmd.OutOrStdout(), true)
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return root.GenPowerShellCompletion(cmd.OutOrStdout())
			default:
				return fmt.Errorf("unsupported shell %q", args[0])
			}
		},
	}
}

func (a *App) newManCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "man DIR",
		Short: "Generate man pages",
		Long:  "Generate roff man pages for dots and its subcommands into DIR.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := os.MkdirAll(args[0], 0o750); err != nil {
				return err
			}
			header := &doc.GenManHeader{
				Title:   "DOTS",
				Section: "1",
				Source:  "dots " + a.version,
				Manual:  "Dots Manual",
			}
			return doc.GenManTree(root, header, args[0])
		},
	}
}
