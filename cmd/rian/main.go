// Command rian is a tiny, Flyway-compatible database migration runner.
//
// The CLI is built on the standard library's flag package (no CLI framework)
// to keep the static binary small. flag treats "-url" and "--url" identically,
// which is exactly the Flyway-compatible behavior Rian wants.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/clarkeandrew/rian/internal/config"
	"github.com/clarkeandrew/rian/internal/db"
	"github.com/clarkeandrew/rian/internal/db/mysql"
	"github.com/clarkeandrew/rian/internal/db/postgres"
	"github.com/clarkeandrew/rian/internal/engine"
)

// version is overridden at build time via -ldflags by the release pipeline.
var version = "0.0.0-dev"

// errUsage signals that a usage problem was already reported to the user.
var errUsage = errors.New("usage error")

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	switch {
	case err == nil:
	case errors.Is(err, errUsage):
		os.Exit(1)
	default:
		fmt.Fprintln(os.Stderr, "rian:", err)
		os.Exit(1)
	}
}

// cli holds the flag set and the values bound to it. All flags are global (the
// same set applies to every command), mirroring Flyway's CLI.
type cli struct {
	fs *flag.FlagSet

	url, user, password string
	table, target       string
	outOfOrder          bool
	validateOnMigrate   bool
	locations           listFlag
	configFiles         listFlag
	placeholders        mapFlag
	showVersion         bool
}

func newCLI() *cli {
	c := &cli{
		fs:           flag.NewFlagSet("rian", flag.ContinueOnError),
		placeholders: mapFlag{},
	}
	c.fs.StringVar(&c.url, "url", "", "JDBC-style database URL (e.g. jdbc:postgresql://host:5432/db)")
	c.fs.StringVar(&c.user, "user", "", "database user")
	c.fs.StringVar(&c.password, "password", "", "database password")
	c.fs.StringVar(&c.table, "table", "", "schema history table name")
	c.fs.StringVar(&c.target, "target", "", "highest version to apply ('latest' or empty = no limit)")
	c.fs.BoolVar(&c.outOfOrder, "outOfOrder", false, "allow applying versions below the latest applied one")
	c.fs.BoolVar(&c.validateOnMigrate, "validateOnMigrate", true, "validate the recorded history before migrating")
	c.fs.Var(&c.locations, "locations", "migration locations (comma-separated)")
	c.fs.Var(&c.configFiles, "configFiles", "flyway.conf files to load (comma-separated)")
	c.fs.Var(&c.placeholders, "placeholders", "placeholder values (key=value, comma-separated)")
	c.fs.BoolVar(&c.showVersion, "version", false, "print the version")
	c.fs.BoolVar(&c.showVersion, "v", false, "print the version (shorthand)")
	return c
}

func (c *cli) usage() string {
	var b strings.Builder
	b.WriteString(`Rian is a tiny, Flyway-compatible database migration runner.

Usage:
  rian <command> [flags]

Commands:
  migrate     Apply pending migrations
  info        Show the status of each migration
  validate    Validate applied migrations against the local ones
  baseline    Baseline an existing database at the configured version
  repair      Remove failed history entries and realign checksums

Flags (single-dash Flyway style and double-dash both work):
`)
	c.fs.SetOutput(&b)
	c.fs.PrintDefaults()
	return b.String()
}

// run parses args and executes the requested command. Flags may appear before
// or after the command; the first argument that does not start with '-' is the
// command.
func run(args []string, stdout, stderr io.Writer) error {
	cmd, rest := splitCommand(args)
	c := newCLI()
	c.fs.SetOutput(stderr)
	c.fs.Usage = func() {} // errors print just the message; help is handled below
	if err := c.fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, c.usage())
			return nil
		}
		fmt.Fprintln(stderr, "run 'rian -h' for usage")
		return errUsage
	}
	if c.showVersion {
		fmt.Fprintf(stdout, "rian version %s\n", version)
		return nil
	}
	if cmd == "" {
		fmt.Fprint(stdout, c.usage())
		return nil
	}
	switch cmd {
	case "migrate", "info", "validate", "baseline", "repair":
	default:
		return fmt.Errorf("unknown command %q (run 'rian -h' for usage)", cmd)
	}

	ctx := context.Background()
	cfg, err := c.resolveConfig(stderr)
	if err != nil {
		return err
	}
	conn, err := connect(ctx, cfg)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	eng := engine.New(conn, cfg)

	switch cmd {
	case "migrate":
		return runMigrate(ctx, eng, stdout)
	case "info":
		return runInfo(ctx, eng, stdout)
	case "validate":
		return runValidate(ctx, eng, stdout, stderr)
	case "baseline":
		return runBaseline(ctx, eng, stdout)
	default:
		return runRepair(ctx, eng, stdout)
	}
}

// splitCommand returns the first non-flag argument as the command and the
// remaining arguments. Flag values given as separate tokens ("-user sa") must
// come after the command; the '=' form ("-user=sa") works anywhere, as in
// Flyway.
func splitCommand(args []string) (string, []string) {
	for i, a := range args {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			rest := make([]string, 0, len(args)-1)
			rest = append(rest, args[:i]...)
			rest = append(rest, args[i+1:]...)
			return a, rest
		}
	}
	return "", args
}

// resolveConfig merges config files, env, and the CLI flags (precedence: flags >
// env > file) and prints any warnings to stderr. Only flags that were actually
// set on the command line override lower-precedence sources.
func (c *cli) resolveConfig(stderr io.Writer) (config.Config, error) {
	set := map[string]bool{}
	c.fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	fl := config.Flags{ConfigFiles: c.configFiles, Placeholders: c.placeholders}
	if set["url"] {
		fl.URL = &c.url
	}
	if set["user"] {
		fl.User = &c.user
	}
	if set["password"] {
		fl.Password = &c.password
	}
	if set["table"] {
		fl.Table = &c.table
	}
	if set["target"] {
		fl.Target = &c.target
	}
	if set["outOfOrder"] {
		fl.OutOfOrder = &c.outOfOrder
	}
	if set["validateOnMigrate"] {
		fl.ValidateOnMigrate = &c.validateOnMigrate
	}
	if set["locations"] {
		v := []string(c.locations)
		fl.Locations = &v
	}

	cfg, err := config.Load(fl, os.Environ())
	for _, w := range cfg.Warnings {
		fmt.Fprintln(stderr, "rian: warning:", w)
	}
	return cfg, err
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

func runMigrate(ctx context.Context, eng *engine.Engine, stdout io.Writer) error {
	res, err := eng.Migrate(ctx)
	if err != nil {
		return err
	}
	if len(res.Applied) == 0 {
		fmt.Fprintln(stdout, "Schema is up to date. No migration necessary.")
		return nil
	}
	fmt.Fprintf(stdout, "Successfully applied %d migration(s):\n", len(res.Applied))
	for _, m := range res.Applied {
		fmt.Fprintf(stdout, "  %s\n", m.Script)
	}
	return nil
}

func runInfo(ctx context.Context, eng *engine.Engine, stdout io.Writer) error {
	entries, err := eng.Info(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "Type\tVersion\tDescription\tStatus")
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Type, e.Version, e.Description, e.Status)
	}
	return tw.Flush()
}

func runValidate(ctx context.Context, eng *engine.Engine, stdout, stderr io.Writer) error {
	problems, err := eng.Validate(ctx)
	if err != nil {
		return err
	}
	if len(problems) == 0 {
		fmt.Fprintln(stdout, "Validation successful.")
		return nil
	}
	for _, p := range problems {
		fmt.Fprintln(stderr, " -", p.String())
	}
	return fmt.Errorf("validation failed: %d problem(s)", len(problems))
}

func runBaseline(ctx context.Context, eng *engine.Engine, stdout io.Writer) error {
	if err := eng.Baseline(ctx); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Baseline recorded.")
	return nil
}

func runRepair(ctx context.Context, eng *engine.Engine, stdout io.Writer) error {
	res, err := eng.Repair(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Repair complete: removed %d failed entries, realigned %d checksums.\n",
		res.RemovedFailed, res.AlignedChecksums)
	return nil
}

// listFlag is a repeatable, comma-separated string-list flag (like Flyway's
// locations lists).
type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }

func (l *listFlag) Set(v string) error {
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			*l = append(*l, s)
		}
	}
	return nil
}

// mapFlag is a repeatable key=value flag accepting comma-separated pairs
// ("k1=v1,k2=v2"); a value may itself contain '=' (split is on the first one).
type mapFlag map[string]string

func (m mapFlag) String() string {
	pairs := make([]string, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, k+"="+v)
	}
	return strings.Join(pairs, ",")
}

func (m mapFlag) Set(v string) error {
	for _, pair := range strings.Split(v, ",") {
		k, val, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return fmt.Errorf("expected key=value, got %q", pair)
		}
		m[strings.TrimSpace(k)] = val
	}
	return nil
}
