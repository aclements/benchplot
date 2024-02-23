// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plot

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

type gnuplotter struct {
	*Plot
	code strings.Builder

	confidence float64
	colorScale func(point) int
}

func (p *Plot) GnuplotCode() (string, error) {
	return (&gnuplotter{Plot: p}).plot()
}

func (p *gnuplotter) plot() (string, error) {
	pts := p.points
	p.confidence = 0.95

	if len(pts) == 0 {
		return "", fmt.Errorf("no data")
	}

	if pointsKinds(pts, AesX)&kindContinuous == 0 {
		// TODO: Bar chart
		return "", fmt.Errorf("non-numeric X data not supported")
	}
	if pointsKinds(pts, AesY)&kindContinuous == 0 {
		// TODO: Horizontal bar chart?
		return "", fmt.Errorf("non-numeric Y data not supported")
	}
	rowScale, nRows := ordScale(pts, AesRow)
	colScale, nCols := ordScale(pts, AesCol)
	multiplot := nRows > 1 || nCols > 1
	p.colorScale, _ = ordScale(pts, AesColor)

	if multiplot {
		// Configure multiplot
		fmt.Fprintf(&p.code, "set multiplot layout %d,%d columnsfirst\n", nRows, nCols)
	}

	// Set log scales
	setLogScale := func(aes Aes, name string) {
		if base := p.logScale.Get(aes); base != 0 {
			fmt.Fprintf(&p.code, "set logscale %s %d\n", name, base)
		}
	}
	setLogScale(AesX, "x")
	setLogScale(AesY, "y")

	// Sort the points in the order the data must be emitted.
	slices.SortFunc(pts, func(a, b point) int {
		if c := a.Get(AesCol).compare(b.Get(AesCol)); c != 0 {
			return c
		}
		if c := a.Get(AesRow).compare(b.Get(AesRow)); c != 0 {
			return c
		}
		if c := a.Get(AesColor).compare(b.Get(AesColor)); c != 0 {
			return c
		}
		// For a line plot, X must be sorted numerically.
		return cmp.Compare(a.Get(AesX).val, b.Get(AesX).val)
	})

	// Emit plots
	type rowCol struct{ row, col int }
	plots, _ := groupBy(pts, func(pt point) rowCol {
		return rowCol{rowScale(pt), colScale(pt)}
	})
	for col := range nCols {
		for row := range nRows {
			p.onePlot(plots[rowCol{row, col}])
		}
	}

	if multiplot {
		fmt.Fprintf(&p.code, "unset multiplot\n")
	}

	return p.code.String(), nil
}

func pointAesGetter(aes Aes) func(pt point) value {
	return func(pt point) value {
		return pt.Get(aes)
	}
}

func (p *gnuplotter) onePlot(pts []point) {
	if len(pts) == 0 {
		// Skip this plot.
		fmt.Fprintf(&p.code, "set multiplot next\n")
		return
	}

	// Scale the values
	xScale, _, _, xLabel, _ := p.continuousScale(pts, AesX)
	yScale, _, _, yLabel, _ := p.continuousScale(pts, AesY)

	// Set axis labels
	fmt.Fprintf(&p.code, "set xlabel %s\n", gpString(xLabel))
	fmt.Fprintf(&p.code, "set ylabel %s\n", gpString(yLabel))

	// Emit point data and build plot command
	var plotArgs []string
	var data strings.Builder
	anyRange := false
	sliceBy(pts, pointAesGetter(AesColor),
		func(color value, pts []point) {
			colorIdx := p.colorScale(pts[0]) + 1

			// TODO: Should this be done up front? Then continuousScale would
			// have to understand summaries, but that's fine.
			pts, _ = transformSummarize(pts, AesY, p.confidence)

			// TODO: Do something with the warnings. Allow configuring
			// confidence.
			haveRange := false
			for _, pt := range pts {
				if !math.IsInf(pt.Get(AesY).summary.Lo, 0) {
					haveRange, anyRange = true, true
					break
				}
			}

			// Emit range
			if haveRange {
				plotArg := fmt.Sprintf("'-' using 1:2:3 with filledcurves title '' fc linetype %d fs transparent solid 0.25", colorIdx)
				plotArgs = append(plotArgs, plotArg)

				for _, pt := range pts {
					x := pt.Get(AesX).val
					y := pt.Get(AesY).summary
					if !math.IsInf(y.Lo, 0) {
						fmt.Fprintf(&data, "%g %g %g\n", xScale(x), yScale(y.Lo), yScale(y.Hi))
					}
				}
				fmt.Fprintf(&data, "e\n")
			}

			// Emit center curve.
			plotArg := fmt.Sprintf("'-' using 1:2 with lp title %s linecolor %d", gpString(color.key.StringValues()), colorIdx)
			plotArgs = append(plotArgs, plotArg)
			for _, pt := range pts {
				fmt.Fprintf(&data, "%g %g\n", xScale(pt.Get(AesX).val), yScale(pt.Get(AesY).val))
			}
			fmt.Fprintf(&data, "e\n")
		})

	if anyRange {
		// Add a legend entry for the range.
		plotArg := fmt.Sprintf("1/0 with filledcurves title '%v%% confidence' fc linetype 0 fs transparent solid 0.25", p.confidence*100)
		plotArgs = append(plotArgs, plotArg)
	}

	fmt.Fprintf(&p.code, "plot %s\n", strings.Join(plotArgs, ", "))
	p.code.WriteString(data.String())
}

// gpString returns s escaped for Gnuplot
func gpString(s string) string {
	// I can't find any documentation on Gnuplot's escape syntax, but as far as
	// I can tell, it's compatible with Go's escaping rules.
	return strconv.Quote(s)
}
