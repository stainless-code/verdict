package dashboard

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
)

// Flag names and defaults. The watch defaults are the dashboard's own
// (2 s ticks, a 60 s sweep): a terminal left open on a busy root should not
// stat every bundle every second. The validation rules are serve's.
const (
	flagRoot     = "--root"
	flagOnce     = "--once"
	flagWidth    = "--width"
	flagHeight   = "--height"
	flagJSON     = "--json"
	flagNoWatch  = "--no-watch"
	flagInterval = "--interval"
	flagSweep    = "--sweep"
	flagCheck    = "--check"
	flagTab      = "--tab"

	defaultInterval  = 2 * time.Second
	defaultSweep     = 60 * time.Second
	defaultCheck     = "critical"
	minWatchInterval = 100 * time.Millisecond
)

// options are the parsed flags.
type options struct {
	root, repo, check string
	interval, sweep   time.Duration
	width, height     int
	tab               tab
	once, noWatch     bool
}

// parseFlags reads the flags left to right; the one positional is [repo].
func parseFlags(r *cli.Runner, args []string) (options, int, bool) {
	opts := options{check: defaultCheck, interval: defaultInterval, sweep: defaultSweep}
	for i := 0; i < len(args); i++ {
		next, code, done := parseFlag(r, &opts, args, i)
		if done {
			return opts, code, true
		}
		i = next
	}
	if opts.sweep < opts.interval {
		return opts, usage(r, "dashboard: --sweep must be at least --interval"), true
	}
	return opts, 0, false
}

// parseFlag reads the argument at args[i] and returns the last index it used.
func parseFlag(r *cli.Runner, opts *options, args []string, i int) (next, code int, done bool) {
	switch name := args[i]; name {
	case flagJSON:
		return i, 0, false
	case flagOnce:
		opts.once = true
		return i, 0, false
	case flagNoWatch:
		opts.noWatch = true
		return i, 0, false
	case flagRoot, flagWidth, flagHeight, flagInterval, flagSweep, flagCheck, flagTab:
		value, next, uerr := cli.FlagValue(args, i, name, flagWants[name])
		if uerr != nil {
			return i, r.ReportUsage(uerr), true
		}
		code, done := flagSetters[name](r, opts, value)
		return next, code, done
	default:
		code, done := positional(r, opts, name)
		return i, code, done
	}
}

// positional takes the one optional [repo]; anything else is argv shape.
func positional(r *cli.Runner, opts *options, arg string) (int, bool) {
	if strings.HasPrefix(arg, "-") {
		return usage(r, "unknown flag '"+arg+"' for dashboard (want --root, --once, --tab, --width, --height, "+
			"--check, --interval, --sweep, --no-watch, --json)"), true
	}
	if opts.repo != "" {
		return usage(r, "dashboard: takes one optional <owner/repo>; got '"+opts.repo+"' and '"+arg+"'"), true
	}
	opts.repo = arg
	return 0, false
}

var flagWants = map[string]string{
	flagRoot: "<dir>", flagWidth: "<n>", flagHeight: "<n>", flagInterval: "<duration>", flagSweep: "<duration>",
	flagCheck: "critical|important|informational", flagTab: strings.Join(tabNames[:], "|"),
}

var flagSetters = map[string]func(r *cli.Runner, opts *options, value string) (int, bool){
	flagRoot: func(r *cli.Runner, opts *options, value string) (int, bool) {
		if value == "" {
			return usage(r, "dashboard: --root wants a non-empty directory"), true
		}
		opts.root = value
		return 0, false
	},
	flagWidth: func(r *cli.Runner, opts *options, value string) (int, bool) {
		return applySize(r, flagWidth, value, minCols, maxCols, &opts.width)
	},
	flagHeight: func(r *cli.Runner, opts *options, value string) (int, bool) {
		return applySize(r, flagHeight, value, minRows, maxRows, &opts.height)
	},
	flagInterval: func(r *cli.Runner, opts *options, value string) (int, bool) {
		return applyDuration(r, flagInterval, value, &opts.interval)
	},
	flagSweep: func(r *cli.Runner, opts *options, value string) (int, bool) {
		return applyDuration(r, flagSweep, value, &opts.sweep)
	},
	flagCheck: func(r *cli.Runner, opts *options, value string) (int, bool) {
		if !core.ValidTier(value) {
			return usage(r, "dashboard: unknown --check tier '"+value+"' (want critical|important|informational)"), true
		}
		opts.check = value
		return 0, false
	},
	flagTab: func(r *cli.Runner, opts *options, value string) (int, bool) {
		i := slices.Index(tabNames[:], value)
		if i < 0 {
			return usage(r, "dashboard: unknown --tab '"+value+"' (want "+flagWants[flagTab]+")"), true
		}
		opts.tab = tab(i)
		return 0, false
	},
}

func applySize(r *cli.Runner, name, value string, lo, hi int, target *int) (int, bool) {
	n, err := strconv.Atoi(value)
	if err != nil || n < lo || n > hi {
		return usage(r, "dashboard: "+name+" wants a whole number from "+strconv.Itoa(lo)+" to "+strconv.Itoa(hi)), true
	}
	*target = n
	return 0, false
}

func applyDuration(r *cli.Runner, name, value string, target *time.Duration) (int, bool) {
	d, err := time.ParseDuration(value)
	if err != nil || d < minWatchInterval {
		return usage(r, "dashboard: "+name+" wants a duration of at least 100ms"), true
	}
	*target = d
	return 0, false
}

func usage(r *cli.Runner, message string) int {
	return r.ReportUsage(&cli.UsageError{Message: core.Msgf("%s", message)})
}
