// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/aclements/benchplot/internal/plot"
	"golang.org/x/perf/benchfmt"
	"golang.org/x/perf/benchproc"
)

func main() {
	if err := benchplot(os.Stdout, os.Stderr, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

type aesFlag struct {
	aes plot.Aes
	def string
	doc string
}

var aesFlags = []aesFlag{
	{plot.AesX, ".fullname", "map values of `projection` to the X axis"},
	{plot.AesY, ".value", "map values of `projection` to the Y axis"},
	{plot.AesColor, ".residue", "map values of `projection` to color"},
	{plot.AesRow, ".unit", "map values of `projection` to facet rows"},
	{plot.AesCol, "", "map values of `projection` to facet columns"},
}

type transformOpt struct {
	doc string
	do  func(p *plot.Plot) error
}

var transformOpts = map[string]transformOpt{
	"compare": {"normalize each value against the first value at the same X",
		(*plot.Plot).TransformCompare},
}

func benchplot(w, wErr io.Writer, args []string) error {
	flags := flag.NewFlagSet("", flag.ExitOnError)
	flags.SetOutput(wErr)

	// We break the flags into a few subsets for help printing.
	mainFlagSet := flag.NewFlagSet("", 0)
	mainFlagSet.SetOutput(wErr)
	aesFlagSet := flag.NewFlagSet("", 0)
	aesFlagSet.SetOutput(wErr)

	flags.Usage = func() {
		fmt.Fprintf(wErr, `Usage: benchplot [flags] inputs...
`)
		mainFlagSet.PrintDefaults()

		// Print aesthetic flags in natural order.
		fmt.Fprintf(wErr, "\nAesthetic flags:\n")
		for _, f := range aesFlags {
			fset := flag.NewFlagSet("", 0)
			fset.SetOutput(wErr)
			fset.String(f.aes.Name(), f.def, f.doc)
			fset.PrintDefaults()
		}
		fmt.Fprintf(wErr, `
For the syntax of projections, see

  https://pkg.go.dev/golang.org/x/perf/benchproc/syntax

In addition, any projection may be one of the following:

  .unit    The unit of each benchmark-reported metric
  .value   The value of the metric corresponding to .unit
  .residue All fields that were not in some other projection
`)

		// Print transforms.
		fmt.Fprintf(wErr, "\nTransformations:\n")
		var names []string
		for name := range transformOpts {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			fmt.Fprintf(wErr, "  %s\n    \t%s\n", name, transformOpts[name].doc)
		}
	}

	// Register aesthetic flags.
	type aesFlagReg struct {
		aesFlag

		flagString *string

		dv   bool
		proj *benchproc.Projection
	}
	var aesFlagRegs = make([]aesFlagReg, 0, len(aesFlags))
	for _, f := range aesFlags {
		aesFlagRegs = append(aesFlagRegs,
			aesFlagReg{
				aesFlag:    f,
				flagString: aesFlagSet.String(f.aes.Name(), f.def, f.doc),
			})
	}

	// Register main flags.
	flagIgnore := mainFlagSet.String("ignore", "", "ignore variations in `keys`")
	flagFilter := mainFlagSet.String("filter", "*", "use only benchmarks matching benchfilter `query`")
	// This is a convenience filter, since if you want to filter on anything,
	// it's usually this.
	flagUnits := mainFlagSet.String("unit", "", "comma-separated list of `units` to show")
	flagLogScale := mainFlagSet.String("log-scale", "", "comma-separated `list` of options to plot on a log scale\nUse name:base to set a log base other than 10")
	flagTransform := mainFlagSet.String("transform", "", "comma-separated `list` of data transformations")

	// Merge flag sets.
	mergeFlags := func(dst, src *flag.FlagSet) {
		src.VisitAll(func(f *flag.Flag) {
			dst.Var(f.Value, f.Name, f.Usage)
		})
	}
	mergeFlags(flags, mainFlagSet)
	mergeFlags(flags, aesFlagSet)

	// Parse flags. Finally!
	flags.Parse(args)
	if flags.NArg() == 0 {
		flags.Usage()
		os.Exit(2)
	}

	config := plot.NewConfig()

	// Parse filter options.
	filter, err := benchproc.NewFilter(*flagFilter)
	if err != nil {
		return fmt.Errorf("parsing -filter: %s", err)
	}
	var keepUnits map[string]bool
	if *flagUnits != "" {
		keepUnits = make(map[string]bool)
		for _, unit := range strings.Split(*flagUnits, ",") {
			keepUnits[unit] = true
		}
	}

	// Parse projection options.
	var parser benchproc.ProjectionParser
	var parseResidue []*aesFlagReg
	for i := range aesFlagRegs {
		f := &aesFlagRegs[i]
		switch *f.flagString {
		case ".unit":
			// TODO: Ideally you should be able to combine .unit with other
			// projection bits. I remember specifically not wanting this for
			// benchstat, so maybe this has to be an option to the parser.
			proj, _, _ := parser.ParseWithUnit("", filter)
			f.proj = proj
		case ".value":
			f.dv = true
		case ".residue":
			parseResidue = append(parseResidue, f)
		default:
			proj, err := parser.Parse(*f.flagString, filter)
			if err != nil {
				return fmt.Errorf("parsing -%s: %s", f.aes.Name(), err)
			}
			f.proj = proj
		}
	}

	// Process projection residue.
	_, err = parser.Parse(*flagIgnore, filter)
	if err != nil {
		return fmt.Errorf("parsing -ignore: %s", err)
	}
	residue := parser.Residue()
	if len(parseResidue) > 0 {
		// If any of the projections are the residue, set them and
		// clear the explicit residue.
		//
		// TODO: Otherwise, report residue mismatches.
		for _, f := range parseResidue {
			f.proj = residue
		}
		residue = nil
	}

	// Bind projections to aesthetics.
	for _, f := range aesFlagRegs {
		if f.dv {
			config.SetDV(f.aes)
		} else {
			config.SetIV(f.aes, f.proj)
		}
	}

	// Parse log-scale option.
	if *flagLogScale != "" {
		for _, opt := range strings.Split(*flagLogScale, ",") {
			opt, baseStr, hasBase := strings.Cut(opt, ":")
			aes, ok := plot.AesFromName(opt)
			if !ok {
				return fmt.Errorf("unknown option %s in -log-scale=%s", opt, *flagLogScale)
			}
			base := 10
			if hasBase {
				base2, err := strconv.ParseInt(baseStr, 10, 0)
				if err != nil {
					return fmt.Errorf("bad base %s in -log-scale=%s: %w", baseStr, *flagLogScale, err)
				}
				base = int(base2)
			}
			config.SetLogScale(aes, base)
		}

	}

	// Parse transforms.
	var transforms []func(p *plot.Plot) error
	if *flagTransform != "" {
		for _, opt := range strings.Split(*flagTransform, ",") {
			t, ok := transformOpts[opt]
			if !ok {
				return fmt.Errorf("unknown transform %s", opt)
			}
			transforms = append(transforms, t.do)
		}
	}

	// Read inputs.
	var errors []errorAt
	var nParsed, nFiltered, nUnitFiltered int
	pl, err := plot.NewPlot(config)
	if err != nil {
		return err
	}
	files := benchfmt.Files{Paths: flags.Args(), AllowStdin: true, AllowLabels: true}
	for files.Scan() {
		switch rec := files.Result(); rec := rec.(type) {
		case *benchfmt.SyntaxError:
			// Non-fatal result parse error. Warn
			// but keep going.
			fmt.Fprintln(wErr, rec)
		case *benchfmt.Result:
			nParsed++
			if ok, err := filter.Apply(rec); !ok {
				nFiltered++
				if err != nil {
					// Print the reason we rejected this result.
					fmt.Fprintln(wErr, err)
				}
				continue
			}
			if keepUnits != nil {
				j := 0
				for _, val := range rec.Values {
					if keepUnits[val.Unit] || (val.OrigUnit != "" && keepUnits[val.OrigUnit]) {
						rec.Values[j] = val
						j++
					}
				}
				rec.Values = rec.Values[:j]
				if j == 0 {
					nUnitFiltered++
					continue
				}
			}

			pl.Add(rec)
		}
	}
	if err := files.Err(); err != nil {
		return err
	}
	if nParsed == 0 {
		return fmt.Errorf("no data")
	} else if nUnitFiltered == nParsed {
		return fmt.Errorf("no data has units %s", *flagUnits)
	} else if nUnitFiltered+nFiltered == nParsed {
		return fmt.Errorf("all data filtered")
	}
	if len(errors) > 0 {
		// No need to sort right now because they're already in order.
		return errorsAt(errors)
	}
	if nFiltered > 0 || nUnitFiltered > 0 {
		fmt.Fprintf(wErr, "%d records did not match -filter, %d records did not match -unit\n", nFiltered, nUnitFiltered)
	}

	// Apply transforms.
	for _, transform := range transforms {
		if err := transform(pl); err != nil {
			return err
		}
	}

	//code, err := plot.GnuplotCode()
	f, err := os.Create("benchplot.png")
	if err != nil {
		return err
	}
	defer f.Close()
	err = pl.Gnuplot("png", f)
	if err != nil {
		return err
	}
	//fmt.Fprint(w, code)
	return nil
}

type errorAt struct {
	file string
	line int
	err  error
}

func (e errorAt) Error() string {
	return fmt.Sprintf("%s:%d: %s", e.file, e.line, e.err.Error())
}

type errorsAt []errorAt

func (e errorsAt) Error() string {
	var b strings.Builder
	for i, err := range e {
		if i == 10 {
			b.WriteString("more errors...")
			break
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(err.Error())
	}
	return b.String()
}
