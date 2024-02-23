// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plot

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/perf/benchproc"
	"golang.org/x/perf/benchunit"
)

func pointsKinds(pts []point, aes Aes) valueKinds {
	kinds := kindAll
	for _, pt := range pts {
		kinds &= pt.Get(aes).kinds
	}
	return kinds
}

// ordScale returns an ordinal scale from aes to [0, bound).
func ordScale(pts []point, aes Aes) (scale func(point) int, bound int) {
	kinds := pointsKinds(pts, aes)

	if kinds&kindDiscrete != 0 {
		// Collect all unique values.
		vals := make(map[benchproc.Key]struct{})
		for _, pt := range pts {
			vals[pt.Get(aes).key] = struct{}{}
		}
		ord := make(map[benchproc.Key]int)
		for i, k := range sortedKeys(vals) {
			ord[k] = i
		}
		return func(pt point) int {
			if idx, ok := ord[pt.Get(aes).key]; ok {
				return idx
			}
			panic("value has unmapped key")
		}, len(ord)
	}

	if kinds&kindContinuous != 0 {
		// Collect all unique values.
		set := make(map[float64]struct{})
		var sl []float64
		for _, pt := range pts {
			val := pt.Get(aes).val
			if _, ok := set[val]; !ok {
				set[val] = struct{}{}
				sl = append(sl, val)
			}
		}
		sort.Float64s(sl)

		ord := make(map[float64]int)
		for i, k := range sl {
			ord[k] = i
		}
		return func(pt point) int {
			if idx, ok := ord[pt.Get(aes).val]; ok {
				return idx
			}
			panic("value has unmapped key")
		}, len(ord)
	}

	panic(aes.Name() + " is neither discrete nor continuous")
}

func (p *Plot) continuousScale(pts []point, aes Aes, rescale bool) (scale func(float64) float64, lo, hi float64, label string, err error) {
	if pointsKinds(pts, aes)&kindContinuous == 0 {
		err = fmt.Errorf("%s data must be numeric", aes.Name())
		return
	}

	// Find bounds.
	for i, pt := range pts {
		val := pt.Get(aes).val
		if i == 0 {
			lo, hi = val, val
		} else {
			lo, hi = min(lo, val), max(hi, val)
		}
	}

	// We only apply scaling to the DV.
	projection := p.aes.Get(aes)
	if len(pts) == 0 || !projection.dv {
		// No-op scale.
		label = projection.String()
		scale = func(v float64) float64 {
			return v
		}
		return
	}

	// Get units.
	//
	// We can wind up with multiple units if, say, -color is configured to
	// .unit. In that case, we combine all of the units.
	unitNames := make([]string, 0, 1)
	var unitSet map[string]struct{} // Lazily initialized.
	for i, pt := range pts {
		n := pt.Get(p.unitAes).key.Get(p.unitField)
		if i == 0 {
			unitNames = append(unitNames, n)
			continue
		}
		if n == unitNames[0] {
			continue
		}
		// Tricky case: there are multiple units
		if unitSet == nil {
			// Lazily initialize the set.
			unitSet = make(map[string]struct{})
			unitSet[unitNames[0]] = struct{}{}
		}
		if _, ok := unitSet[n]; ok {
			continue
		}
		unitSet[n] = struct{}{}
		unitNames = append(unitNames, n)
	}

	prefix := ""
	if rescale {
		// Get unit class.
		cls := benchunit.Decimal
		if len(unitNames) == 1 {
			cls = benchunit.ClassOf(unitNames[0])
		}

		// Construct scaler. We pass only the highest value. Otherwise this will try
		// to pick a scale that keeps precision for the *smallest* value, which
		// isn't what you want on an axis.
		scaler := benchunit.CommonScale([]float64{hi}, cls)
		scale = func(v float64) float64 {
			return v / scaler.Factor
		}
		prefix = scaler.Prefix
	} else {
		// The caller has requested that we not rescale values (probably because
		// the plotter it's talking to has that ability).
		scale = func(v float64) float64 {
			return v
		}
	}

	// Construct label.
	//
	// TODO: This could result in some weird looking units. Ideally benchunit
	// would have something that could say "this unit's base quantity is time
	// (and thus base unit is seconds)", allowing us to scale and represent it,
	// though then we'd probably need to compute out own tick marks.
	labels := make([]string, 0, 1)
	for _, n := range unitNames {
		labels = append(labels, prefix+n)
	}
	label = strings.Join(labels, ", ")

	return
}
