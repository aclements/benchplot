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

	"golang.org/x/perf/benchmath"
	"golang.org/x/perf/benchunit"
)

type gnuplotter struct {
	*Plot
	code strings.Builder

	confidence float64
	colorScale func(value) int
}

func (p *Plot) GnuplotCode() (string, error) {
	return (&gnuplotter{Plot: p}).plot()
}

func (p *gnuplotter) plot() (string, error) {
	p.confidence = 0.95

	if len(p.points) == 0 {
		return "", fmt.Errorf("no data")
	}

	if p.kinds.Get(AesX)&kindContinuous == 0 {
		// TODO: Bar chart
		return "", fmt.Errorf("non-numeric X data not supported")
	}
	if p.kinds.Get(AesY)&kindContinuous == 0 {
		// TODO: Horizontal bar chart?
		return "", fmt.Errorf("non-numeric Y data not supported")
	}
	_, nRows := p.ordScale(AesRow)
	_, nCols := p.ordScale(AesCol)
	multiplot := nRows > 1 || nCols > 1
	p.colorScale, _ = p.ordScale(AesColor)

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
	slices.SortFunc(p.points, func(a, b point) int {
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
	pts := p.points
	sliceBy(pts, pointAesGetter(AesCol),
		func(col value, pts []point) {
			sliceBy(pts, pointAesGetter(AesRow),
				func(row value, pts []point) {
					p.onePlot(pts)
				})
		})

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
	var unitName string
	if p.unitField != nil {
		// TODO: What happens if, say, -color is configured to .unit? Then it
		// won't be consistent across pts. But it also pretty unclear what to do
		// with, say, -color .unit -y .value. What do we label the Y axis? It
		// probably has to be some combination of the units.
		unitName = pts[0].Get(p.unitAes).key.Get(p.unitField)
	}

	// Scale the values
	//
	// TODO: This could result in some weird looking units. Ideally benchunit
	// would have something that could say "this unit's base quantity is time
	// (and thus base unit is seconds)", allowing us to scale and represent it,
	// though then we'd probably need to compute out own tick marks.
	scaler := func(aes Aes) benchunit.Scaler {
		projection := p.aes.Get(aes)
		if !projection.dv || unitName == "" {
			return benchunit.NoOpScaler
		}

		cls := benchunit.ClassOf(unitName)

		var vals []float64
		for _, pt := range p.points {
			vals = append(vals, pt.Get(aes).val)
		}

		return benchunit.CommonScale(vals, cls)
	}
	xScaler := scaler(AesX)
	yScaler := scaler(AesY)

	// Set axis labels
	fmt.Fprintf(&p.code, "set xlabel %s\n", gpString(xScaler.Prefix+p.Label(pts[0], AesX)))
	fmt.Fprintf(&p.code, "set ylabel %s\n", gpString(yScaler.Prefix+p.Label(pts[0], AesY)))

	// Emit point data and build plot command
	var plotArgs []string
	var data strings.Builder
	type plotPoint struct {
		x float64
		y benchmath.Summary
	}
	var plotPoints []plotPoint // Scratch slice
	var ys []float64           // Scratch slice
	anyRange := false
	sliceBy(pts, pointAesGetter(AesColor),
		func(color value, pts []point) {
			colorIdx := p.colorScale(color)

			// Construct points on this curve
			plotPoints = plotPoints[:0]
			haveRange := false
			sliceBy(pts, pointAesGetter(AesX),
				func(x value, pts []point) {
					// TODO: Do something with the warnings. Allow configuring confidence.
					ys = ys[:0]
					for _, p := range pts {
						ys = append(ys, p.Get(AesY).val)
					}
					sample := benchmath.NewSample(ys, &benchmath.DefaultThresholds)
					summary := benchmath.AssumeNothing.Summary(sample, p.confidence)

					plotPoints = append(plotPoints, plotPoint{x.val, summary})
					if !math.IsInf(summary.Lo, 0) {
						haveRange = true
					}
				})

			// Emit range
			if haveRange {
				plotArg := fmt.Sprintf("'-' using 1:2:3 with filledcurves title '' fc linetype %d fs transparent solid 0.25", colorIdx)
				plotArgs = append(plotArgs, plotArg)

				for _, pt := range plotPoints {
					if !math.IsInf(pt.y.Lo, 0) {
						fmt.Fprintf(&data, "%g %g %g\n", pt.x/xScaler.Factor, pt.y.Lo/yScaler.Factor, pt.y.Hi/yScaler.Factor)
					}
				}
				fmt.Fprintf(&data, "e\n")
				anyRange = true
			}

			// Emit center curve.
			plotArg := fmt.Sprintf("'-' using 1:2 with lp title %s linecolor %d", gpString(color.key.StringValues()), colorIdx)
			plotArgs = append(plotArgs, plotArg)
			for _, pt := range plotPoints {
				fmt.Fprintf(&data, "%g %g\n", pt.x/xScaler.Factor, pt.y.Center/yScaler.Factor)
			}
			fmt.Fprintf(&data, "e\n")

			colorIdx++
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
