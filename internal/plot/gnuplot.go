// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plot

import (
	"bytes"
	"cmp"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

type gnuplotter struct {
	*Plot
	code bytes.Buffer

	confidence float64
	colorScale func(point) int
}

func (p *Plot) Gnuplot(term string, out io.Writer) error {
	pl := gnuplotter{Plot: p}
	if err := pl.plot(term); err != nil {
		return err
	}

	// TODO: Only do this if we're launching a windowed gnuplot
	if false {
		fmt.Fprintf(&pl.code, "pause mouse close\n")
	}
	code := pl.code.Bytes()

	switch term {
	case "":
		_, err := out.Write(code)
		return err
	case "png":
		cmd := exec.Command("gnuplot")
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return fmt.Errorf("creating pipe to gnuplot: %w", err)
		}
		cmd.Stdout = out
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("starting gnuplot: %w", err)
		}
		defer cmd.Process.Kill()
		if _, err := stdin.Write(code); err != nil {
			return fmt.Errorf("writing to gnuplot: %w", err)
		}
		stdin.Close()
		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("gnuplot failed: %w", err)
		}
	}
	return nil
}

func (p *gnuplotter) plot(term string) error {
	pts := p.points
	p.confidence = 0.95

	if len(pts) == 0 {
		return fmt.Errorf("no data")
	}

	if pointsKinds(pts, AesX)&kindContinuous == 0 {
		// TODO: Bar chart
		return fmt.Errorf("non-numeric X data not supported")
	}
	if pointsKinds(pts, AesY)&kindContinuous == 0 {
		// TODO: Horizontal bar chart?
		return fmt.Errorf("non-numeric Y data not supported")
	}
	rowScale, nRows := ordScale(pts, AesRow)
	colScale, nCols := ordScale(pts, AesCol)
	multiplot := nRows > 1 || nCols > 1
	p.colorScale, _ = ordScale(pts, AesColor)

	switch term {
	case "":
		// Just code
	case "png":
		fmt.Fprintf(&p.code, "set terminal pngcairo size %d,%d\n", nCols*640, nRows*480)
	default:
		return fmt.Errorf("unknown output type %s", term)
	}

	if multiplot {
		// Configure multiplot
		fmt.Fprintf(&p.code, "set multiplot layout %d,%d columnsfirst margins char 12,1.0,char 4,char 2 spacing char 10, char 4\n", nRows, nCols)
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
			pts := plots[rowCol{row, col}]
			if multiplot && col == 0 && len(pts) > 0 {
				// Label this row.
				//
				// TODO: This won't work if there are no points in this plot.
				// Maybe I need an inverse scale?
				label := pts[0].Get(AesRow).StringValues()
				fmt.Fprintf(&p.code, "set label 1 %s at char 2, graph 0.5 center rotate by 90\n", gpString(label))
			}
			if multiplot && row == 0 && len(pts) > 0 {
				// Label this column.
				label := pts[0].Get(AesCol).StringValues()
				fmt.Fprintf(&p.code, "set title %s\n", gpString(label))
			}
			p.onePlot(pts)
			fmt.Fprintf(&p.code, "unset label 1\n")
			fmt.Fprintf(&p.code, "unset title\n")
		}
	}

	if multiplot {
		fmt.Fprintf(&p.code, "unset multiplot\n")
	}

	return nil
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

	var reset strings.Builder

	// Let gnuplot print scientific values on tick marks. This is much nicer
	// than putting it on the unit.
	setFormat := func(axis string, aes Aes) (scale func(float64) float64, label string) {
		kinds := pointsKinds(pts, aes)
		// Scale the values. We ask for no rescaling because we'll configure
		// gnuplot to do the scientific scaling for us.
		scale, _, _, label, _ = p.continuousScale(pts, aes, false)
		if kinds&kindRatio != 0 {
			// Format ratios as a percent delta.
			fmt.Fprintf(&p.code, "set format %s '%%+h%%%%'\n", axis)
			scale = func(x float64) float64 { return (x - 1) * 100 }
			label = "delta " + label

			// Always include 0.
			fmt.Fprintf(&p.code, "set %srange [*<0:0<*]\n", axis)

			// Draw a line at 0. The command uses the opposite axis.
			za := "x"
			if aes == AesX {
				za = "y"
			}
			fmt.Fprintf(&p.code, "set %szeroaxis dt 2\n", za)
			fmt.Fprintf(&reset, "unset %szeroaxis\n", za)
		} else {
			// TODO: If the unit class is Binary, use %b%B.
			fmt.Fprintf(&p.code, "set format %s '%%.0s%%c'\n", axis)
		}
		return
	}
	xScale, xLabel := setFormat("x", AesX)
	yScale, yLabel := setFormat("y", AesY)

	// Set axis labels
	fmt.Fprintf(&p.code, "set xlabel %s\n", gpString(xLabel))
	fmt.Fprintf(&p.code, "set ylabel %s\n", gpString(yLabel))

	// TODO: Should this be done up front? Then continuousScale would
	// have to understand summaries, but that's fine.
	//
	// TODO: Do something with the warnings. Allow configuring
	// confidence.
	pts, _ = transformSummarize(pts, AesY, p.confidence)

	// Set up for plotting ratios.
	kinds := pointsKinds(pts, AesY)
	var ratioPos, ratioNeg string
	if p.dvAes == AesY && kinds&kindRatio != 0 {
		// Check that all units have the same "better" direction.
		better := 0
		for i, unitName := range p.pointsUnits(pts) {
			better1 := p.units.GetBetter(unitName)
			if i == 0 {
				better = better1
			} else if better != better1 {
				better = 0
				break
			}
		}

		if better > 0 {
			ratioPos, ratioNeg = "red", "green"
		} else if better < 0 {
			ratioPos, ratioNeg = "green", "red"
		}
	}

	// Emit point data and build plot command. We build this up in several layers.
	const (
		layerPos = iota
		layerNeg
		layerRange
		layerCenter
		maxLayers
	)
	var plotArgs []string
	var data strings.Builder
	anyRange := false
	for layer := range maxLayers {
		sliceBy(pts, pointAesGetter(AesColor),
			func(color value, pts []point) {
				colorIdx := p.colorScale(pts[0]) + 1

				if layer == layerRange {
					haveRange := false
					for _, pt := range pts {
						if !math.IsInf(pt.Get(AesY).summary.Lo, 0) {
							haveRange, anyRange = true, true
							break
						}
					}
					if !haveRange {
						return
					}

					// Emit range
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
					return
				}

				// The other layers all use the same data and just different
				// plot options.
				plotArg := "'-' using 1:2"
				switch layer {
				case layerPos:
					if ratioPos == "" {
						return
					}
					plotArg += " with filledcurves below title '' fs transparent solid 0.1 fc '" + ratioPos + "' lw 0"
				case layerNeg:
					if ratioNeg == "" {
						return
					}
					plotArg += " with filledcurves above title '' fs transparent solid 0.1 fc '" + ratioNeg + "' lw 0"
				case layerCenter:
					plotArg += fmt.Sprintf(" with lp title %s linecolor %d", gpString(color.StringValues()), colorIdx)
				}

				// Emit center curve.
				plotArgs = append(plotArgs, plotArg)
				for _, pt := range pts {
					fmt.Fprintf(&data, "%g %g\n", xScale(pt.Get(AesX).val), yScale(pt.Get(AesY).val))
				}
				fmt.Fprintf(&data, "e\n")
			})
	}

	if anyRange {
		// Add a legend entry for the range.
		plotArg := fmt.Sprintf("1/0 with filledcurves title '%v%% confidence' fc linetype 0 fs transparent solid 0.25", p.confidence*100)
		plotArgs = append(plotArgs, plotArg)
	}

	fmt.Fprintf(&p.code, "plot %s\n", strings.Join(plotArgs, ", "))

	p.code.WriteString(data.String())

	p.code.WriteString(reset.String())
}

// gpString returns s escaped for Gnuplot
func gpString(s string) string {
	// I can't find any documentation on Gnuplot's escape syntax, but as far as
	// I can tell, it's compatible with Go's escaping rules.
	return strconv.Quote(s)
}
