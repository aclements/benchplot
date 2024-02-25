// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plot

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/perf/benchfmt"
	"golang.org/x/perf/benchmath"
	"golang.org/x/perf/benchproc"
)

// TODO: Do something with the residue. Probably complain if two points we're
// averaging have different residues, much like benchstat does.

type Plot struct {
	// aes is the projection for each aesthetic dimension.
	aes aesMap[projection]

	// unitAes is the aesthetic the unit (.unit) is bound to, if any.
	unitAes Aes
	// unitField is the field of unitAes's projection the unit is bound to.
	unitField *benchproc.Field
	// dvAes is the aesthetic the dependent variable (.value) is bound to, if any.
	dvAes Aes

	// logScale is the log base for each aesthetic, or 0 for linear.
	logScale aesMap[int]

	units benchfmt.UnitMetadataMap

	points []point
}

// A projection describes how to map from a [benchfmt.Result] to a value. The
// zero value maps all Results to the zero value.
type projection struct {
	iv        *benchproc.Projection
	ivField   *benchproc.Field // set if iv has exactly one field
	unitField *benchproc.Field // set if iv has a .unit field

	dv bool
}

type value struct {
	kinds valueKinds
	key   benchproc.Key // if kinds & kindDiscrete
	val   float64       // if kinds & kindContinuous

	summary *benchmath.Summary // if kinds & kindSummary
	denom   benchproc.Key      // if kindRatio AND kindDiscrete
}

type valueKinds uint8

const (
	kindDiscrete valueKinds = 1 << iota
	kindContinuous
	kindSummary // Implies kindContinuous
	kindRatio   // Implies kindContinuous OR kindDiscrete

	kindMax

	kindAll = kindMax - 1
)

type point struct {
	aesMap[value]
}

func NewPlot(c *Config) (*Plot, error) {
	// Find unit and DV.
	unitAes, dvAes := aesNone, aesNone
	var unitField *benchproc.Field
	for aes := range aesMax {
		proj := c.aes.Get(aes)
		if proj.unitField != nil {
			if unitAes != aesNone {
				return nil, fmt.Errorf("at most one dimension may show .unit")
			}
			unitAes, unitField = aes, proj.unitField
		}
		if proj.dv {
			if dvAes != aesNone {
				return nil, fmt.Errorf("at most one dimension may show .value")
			}
			dvAes = aes
		}
	}
	// If we have a unit field, then we also need a DV.
	if unitAes != aesNone && dvAes == aesNone {
		return nil, fmt.Errorf(".unit is mapped to the %s dimension, but no dimension shows .value", unitAes.Name())
	}
	if unitAes == aesNone && dvAes != aesNone {
		return nil, fmt.Errorf(".value is mapped to the %s dimension, but no dimension shows .unit", dvAes.Name())
	}

	return &Plot{
		aes:       c.aes.Copy(),
		unitAes:   unitAes,
		unitField: unitField,
		dvAes:     dvAes,
		logScale:  c.logScale,
	}, nil
}

func (p projection) project(r *benchfmt.Result) []value {
	if p.dv {
		panic("cannot project DV")
	}
	if p.iv == nil {
		return []value{{kinds: kindDiscrete}}
	}

	var values []value
	if p.unitField != nil {
		for _, key := range p.iv.ProjectValues(r) {
			values = append(values, value{kinds: kindDiscrete, key: key})
		}
	} else {
		key := p.iv.Project(r)
		values = append(values, value{kinds: kindDiscrete, key: key})
	}

	// Try to parse continuous values, too.
	if p.ivField != nil {
		for i, val := range values {
			s := val.key.Get(p.ivField)
			val, err := strconv.ParseFloat(s, 64)
			if err == nil {
				values[i].kinds |= kindContinuous
				values[i].val = val
			}
		}
	}

	return values
}

func (p projection) String() string {
	if p.dv {
		return ".value"
	}
	if p.iv != nil {
		var out strings.Builder
		for i, field := range p.iv.FlattenedFields() {
			if i > 0 {
				out.WriteByte(',')
			}
			out.WriteString(field.String())
		}
		return out.String()
	}
	return "<nil>"
}

func (v value) String() string {
	if v.kinds&kindDiscrete != 0 {
		if v.kinds&kindRatio != 0 {
			return v.key.String() + " vs " + v.denom.String()
		}
		return v.key.String()
	}
	return fmt.Sprint(v.val)
}

func (v value) StringValues() string {
	if v.kinds&kindDiscrete != 0 {
		if v.kinds&kindRatio != 0 {
			return v.key.StringValues() + " vs " + v.denom.StringValues()
		}
		return v.key.StringValues()
	}
	return fmt.Sprint(v.val)
}

func (v value) compare(v2 value) int {
	if v.kinds&v2.kinds&kindDiscrete != 0 {
		if c := compareKeys(v.key, v2.key); c != 0 {
			return c
		}
		if v.kinds&kindRatio != 0 {
			return compareKeys(v.denom, v2.denom)
		}
		return 0
	}
	if v.kinds&v2.kinds&kindContinuous != 0 {
		return cmp.Compare(v.val, v2.val)
	}
	// Otherwise, put them in kind order.
	for kind := range kindMax {
		if v.kinds&kind != 0 && v2.kinds&kind == 0 {
			return -1
		}
		if v.kinds&kind == 0 && v2.kinds&kind != 0 {
			return 1
		}
	}
	// Something went very wrong.
	panic(fmt.Errorf("incomparable kinds %#x, %#x", v.kinds, v2.kinds))
}

func (p *Plot) Label(pt point, aes Aes) string {
	proj := p.aes.Get(aes)
	if proj.dv {
		return pt.Get(p.unitAes).key.Get(p.unitField)
	}
	return proj.String()
}

func (p *Plot) Add(rec *benchfmt.Result) {
	var pt point
	var fill func(aes Aes)
	fill = func(aes Aes) {
		if aes == aesMax {
			// Add the point.
			p.points = append(p.points, point{pt.aesMap.Copy()})
			return
		}

		proj := p.aes.Get(aes)
		if proj.dv {
			// We fill this in when we find the .unit projection.
			fill(aes + 1)
			return
		}
		// TODO: This is usually a single element slice, making this pretty
		// inefficient.
		vals := proj.project(rec)
		for _, val := range vals {
			if proj.unitField != nil {
				unitName := val.key.Get(proj.unitField)
				val, ok := rec.Value(unitName)
				if !ok {
					// Point is missing this unit. Just drop the point.
					continue
				}
				pt.aesMap.Set(p.dvAes, value{kinds: kindContinuous, val: val})
			}
			pt.aesMap.Set(aes, val)
			fill(aes + 1)
		}
	}
	fill(0)
}

func (p *Plot) SetUnits(units benchfmt.UnitMetadataMap) {
	p.units = units
}

func compareKeys(a, b benchproc.Key) int {
	// TODO: Key should have a Compare method
	if a == b {
		return 0
	}
	if a.Less(b) {
		return -1
	}
	return 1
}

func sortedKeys(set map[benchproc.Key]struct{}) []benchproc.Key {
	sl := make([]benchproc.Key, 0, len(set))
	for k := range set {
		sl = append(sl, k)
	}
	benchproc.SortKeys(sl)
	return sl
}

func sortedValues(set map[value]struct{}) []value {
	sl := make([]value, 0, len(set))
	for v := range set {
		sl = append(sl, v)
	}

	slices.SortFunc(sl, func(a, b value) int {
		return a.compare(b)
	})

	return sl
}

// sliceBy divides slice s into subslices by equal values of grouper.
func sliceBy[T any, U comparable](s []T, grouper func(T) U, doGroup func(U, []T)) {
	if len(s) == 0 {
		return
	}

	start := 0
	startVal := grouper(s[0])
	for i := 1; i < len(s); i++ {
		val := grouper(s[i])
		if val != startVal {
			doGroup(startVal, s[start:i])
			start, startVal = i, val
		}
	}
	doGroup(startVal, s[start:])
}

// groupBy groups the elements of s according to the value of grouper. It
// maintains the order of elements. If possible, it uses subslices of s, but it
// will copy out of s if grouper returns the same value for discontinuous ranges
// of s.
func groupBy[T any, U comparable](s []T, grouper func(T) U) (map[U][]T, []U) {
	out := make(map[U][]T)
	if len(s) == 0 {
		return out, nil
	}

	var keys []U
	start := 0
	startVal := grouper(s[0])
	for i := 1; i < len(s); i++ {
		val := grouper(s[i])
		if val != startVal || i == len(s)-1 {
			if old, ok := out[startVal]; !ok {
				// Use subslice directly.
				out[startVal] = s[start:i]
				keys = append(keys, startVal)
			} else {
				// Copy slice.
				out[startVal] = append(old[:len(old):len(old)], s[start:i]...)
			}
			start, startVal = i, val
		}
	}

	return out, keys
}
