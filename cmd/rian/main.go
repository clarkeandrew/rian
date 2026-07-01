// Command rian is a tiny, Flyway-compatible database migration runner.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/clarkeandrew/rian/internal/config"
	"github.com/clarkeandrew/rian/internal/db"
	"github.com/clarkeandrew/rian/internal/db/mysql"
	"github.com/clarkeandrew/rian/internal/db/postgres"
	"github.com/clarkeandrew/rian/internal/engine"
)

// version is overridden at build time via -ldflags by the release pipeline.
var version = "0.0.0-dev"

func main() {
	root := rootCmd()
	// Accept Flyway-style single-dash long options (e.g. -url, -user) in addition
	// to cobra's --url, so Rian is a drop-in for existing Flyway command lines.
	root.SetArgs(normalizeFlywayArgs(os.Args[1:]))
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rian:", err)
		os.Exit(1)
	}
}

// normalizeFlywayArgs rewrites Flyway-style single-dash long options
// ("-url", "-user=sa") to the double-dash form cobra/pflag expects. Genuine
// short flags ("-h", "-v"), the "--" terminator, already-double-dashed flags,
// and non-flag arguments are left unchanged. Rian defines no multi-character
// short flags, so a single-dash token of length > 2 is always a long option.
func normalizeFlywayArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if len(a) > 2 && a[0] == '-' && a[1] != '-' {
			out[i] = "-" + a
		} else {
			out[i] = a
		}
	}
	return out
}

// cliFlags holds the values bound to the persistent CLI flags.
type cliFlags struct {
	url               string
	user              string
	password          string
	table             string
	target            string
	outOfOrder        bool
	validateOnMigrate bool
	locations         []string
	configFiles       []string
	placeholders      map[string]string
}

func rootCmd() *cobra.Command {
	f := &cliFlags{}
	root := &cobra.Command{
		Use:           "rian",
		Short:         "Rian is a tiny, Flyway-compatible database migration runner",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}

	pf := root.PersistentFlags()
	pf.StringVar(&f.url, "url", "", "JDBC-style database URL (e.g. jdbc:postgresql://host:5432/db)")
	pf.StringVar(&f.user, "user", "", "database user")
	pf.StringVar(&f.password, "password", "", "database password")
	pf.StringVar(&f.table, "table", "", "schema history table name")
	pf.StringVar(&f.target, "target", "", "highest version to apply ('latest' or empty = no limit)")
	pf.BoolVar(&f.outOfOrder, "outOfOrder", false, "allow applying versions below the latest applied one")
	pf.BoolVar(&f.validateOnMigrate, "validateOnMigrate", true, "validate the recorded history before migrating")
	pf.StringSliceVar(&f.locations, "locations", nil, "migration locations (comma-separated)")
	pf.StringSliceVar(&f.configFiles, "configFiles", nil, "flyway.conf files to load")
	pf.StringToStringVar(&f.placeholders, "placeholders", nil, "placeholder values (key=value)")

	root.AddCommand(
		migrateCmd(f),
		infoCmd(f),
		validateCmd(f),
		baselineCmd(f),
		repairCmd(f),
	)
	return root
}

// resolveConfig merges config files, env, and the CLI flags (precedence: flags >
// env > file) and prints any warnings to stderr.
func (f *cliFlags) resolveConfig(cmd *cobra.Command) (config.Config, error) {
	flags := config.Flags{
		ConfigFiles:  f.configFiles,
		Placeholders: f.placeholders,
	}
	if cmd.Flags().Changed("url") {
		flags.URL = &f.url
	}
	if cmd.Flags().Changed("user") {
		flags.User = &f.user
	}
	if cmd.Flags().Changed("password") {
		flags.Password = &f.password
	}
	if cmd.Flags().Changed("table") {
		flags.Table = &f.table
	}
	if cmd.Flags().Changed("target") {
		flags.Target = &f.target
	}
	if cmd.Flags().Changed("outOfOrder") {
		flags.OutOfOrder = &f.outOfOrder
	}
	if cmd.Flags().Changed("validateOnMigrate") {
		flags.ValidateOnMigrate = &f.validateOnMigrate
	}
	if cmd.Flags().Changed("locations") {
		flags.Locations = &f.locations
	}

	cfg, err := config.Load(flags, os.Environ())
	for _, w := range cfg.Warnings {
		fmt.Fprintln(os.Stderr, "rian: warning:", w)
	}
	return cfg, err
}

// open builds the engine for a command: resolve config, then connect.
func (f *cliFlags) open(cmd *cobra.Command, ctx context.Context) (*engine.Engine, func(), error) {
	cfg, err := f.resolveConfig(cmd)
	if err != nil {
		return nil, nil, err
	}
	conn, err := connect(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = conn.Close(ctx) }
	return engine.New(conn, cfg), cleanup, nil
}

// connect selects a dialect from the URL scheme (PostgreSQL or MySQL).
func connect(ctx context.Context, cfg config.Config) (db.Conn, error) {
	switch {
	case strings.HasPrefix(cfg.URL, "jdbc:postgresql:"),
		strings.HasPrefix(cfg.URL, "postgres://"),
		strings.HasPrefix(cfg.URL, "postgresql://"):
		return postgres.Connect(ctx, cfg.URL, cfg.User, cfg.Password)
	case strings.HasPrefix(cfg.URL, "jdbc:mysql:"),
		strings.HasPrefix(cfg.URL, "mysql://"):
		return mysql.Connect(ctx, cfg.URL, cfg.User, cfg.Password)
	case cfg.URL == "":
		return nil, fmt.Errorf("no database url provided (set -url, FLYWAY_URL, or flyway.url)")
	default:
		return nil, fmt.Errorf("unsupported database url %q (supported: PostgreSQL, MySQL)", cfg.URL)
	}
}

func migrateCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			eng, cleanup, err := f.open(cmd, ctx)
			if err != nil {
				return err
			}
			defer cleanup()
			res, err := eng.Migrate(ctx)
			if err != nil {
				return err
			}
			if len(res.Applied) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Schema is up to date. No migration necessary.")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Successfully applied %d migration(s):\n", len(res.Applied))
			for _, m := range res.Applied {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", m.Script)
			}
			return nil
		},
	}
}

func infoCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show the status of each migration",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			eng, cleanup, err := f.open(cmd, ctx)
			if err != nil {
				return err
			}
			defer cleanup()
			entries, err := eng.Info(ctx)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "Type\tVersion\tDescription\tStatus")
			for _, e := range entries {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Type, e.Version, e.Description, e.Status)
			}
			return tw.Flush()
		},
	}
}

func validateCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate applied migrations against the local ones",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			eng, cleanup, err := f.open(cmd, ctx)
			if err != nil {
				return err
			}
			defer cleanup()
			problems, err := eng.Validate(ctx)
			if err != nil {
				return err
			}
			if len(problems) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Validation successful.")
				return nil
			}
			for _, p := range problems {
				fmt.Fprintln(cmd.ErrOrStderr(), " -", p.String())
			}
			return fmt.Errorf("validation failed: %d problem(s)", len(problems))
		},
	}
}

func baselineCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "baseline",
		Short: "Baseline an existing database at the configured version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			eng, cleanup, err := f.open(cmd, ctx)
			if err != nil {
				return err
			}
			defer cleanup()
			if err := eng.Baseline(ctx); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Baseline recorded.")
			return nil
		},
	}
}

func repairCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "repair",
		Short: "Remove failed history entries and realign checksums",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			eng, cleanup, err := f.open(cmd, ctx)
			if err != nil {
				return err
			}
			defer cleanup()
			res, err := eng.Repair(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Repair complete: removed %d failed entries, realigned %d checksums.\n",
				res.RemovedFailed, res.AlignedChecksums)
			return nil
		},
	}
}
