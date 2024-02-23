// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plot

import (
	"fmt"
	"slices"

	"golang.org/x/perf/benchmath"
)

func pointsToSample(pts []point, aes Aes) *benchmath.Sample {
	ys := make([]float64, 0, 16)
	for _, pt := range pts {
		val := pt.Get(aes)
		if val.kinds&kindContinuous == 0 {
			panic("non-continuous " + aes.Name())
		}
		ys = append(ys, val.val)
	}
	return benchmath.NewSample(ys, &benchmath.DefaultThresholds)
}

// transformSummarize groups points that differ only in aes and produces a
// single point for each group where aes is set to a summary of the group.
//
// aes must have kind kindContinuous.
func transformSummarize(pts []point, aes Aes, confidence float64) ([]point, error) {
	kinds := pointsKinds(pts, aes)
	if kinds&kindSummary != 0 {
		// Nothing to do if it's already summaries.
		return pts, nil
	}
	if kinds&kindContinuous == 0 {
		return nil, fmt.Errorf("transformSummarize: %s data must be numeric", aes.Name())
	}

	groups, keys := groupBy(pts, func(pt point) point {
		pt.Set(aes, value{})
		return pt
	})

	// Compute summary for each group. We put this in a slice to avoid
	// allocating each Summary separately.
	summaries := make([]benchmath.Summary, len(keys))
	for i, k := range keys {
		sample := pointsToSample(groups[k], aes)
		summaries[i] = benchmath.AssumeNothing.Summary(sample, confidence)
	}

	// Construct new points.
	out := make([]point, len(keys))
	for i, k := range keys {
		pt := groups[k][0]
		// Keep it as a ratio if the input is.
		kinds := kindContinuous | kindSummary | (kinds & kindRatio)
		summary := &summaries[i]
		v := value{kinds: kinds, val: summary.Center, summary: summary}
		pt.Set(aes, v)
		out[i] = pt
	}

	return out, nil
}

func (p *Plot) TransformCompare() error {
	// TODO: It feels weird to pass AesColor here. Should this be up to what
	// type of plot we're creating?
	pts, err := transformCompare(p.points, AesColor, p.dvAes)
	if err != nil {
		return err
	}
	p.points = pts
	return nil
}

// transformCompare groups points that differ only in aesCompare and aesRatio
// and for each distinct value of aesCompare, treats the first value as a
// baseline and normalizes the aesRatio of all other values of aesCompare
// against that baseline.
//
// TODO: Right now, this collapses each group down to a median and produces only
// continuous values for aes. It really ought to compute summary values.
func transformCompare(pts []point, aesCompare, aesRatio Aes) ([]point, error) {
	if len(pts) == 0 {
		return nil, nil
	}

	if pointsKinds(pts, aesRatio)&kindContinuous == 0 {
		return nil, fmt.Errorf("transformCompare: %s data must be numeric", aesRatio.Name())
	}

	// We want to walk through things in order of aesCompare. Sort it up-front
	// and the grouping operations will keep it sorted.
	slices.SortFunc(pts, func(a, b point) int {
		return a.Get(aesCompare).compare(b.Get(aesCompare))
	})
	// Everything shares the same comparison baseline.
	cmpBase := pts[0].Get(aesCompare)

	groups, keys := groupBy(pts, func(pt point) point {
		pt.Set(aesCompare, value{})
		pt.Set(aesRatio, value{})
		return pt
	})

	median := func(pts []point) float64 {
		// TODO: This does a ton of wasted computation.
		return benchmath.AssumeNothing.Summary(pointsToSample(pts, aesRatio), 1).Center
	}

	var out []point
	for _, k := range keys {
		group := groups[k]

		// If this group doesn't have a baseline, skip it entirely.
		if group[0].Get(aesCompare) != cmpBase {
			continue
		}

		// Group by value of aesCompare.
		cmpGroups, cmpKeys := groupBy(group, func(pt point) value {
			return pt.Get(aesCompare)
		})

		// Create a point for each value of aesCompare, normalized to the first.
		baseline := median(cmpGroups[cmpBase])
		for _, ck := range cmpKeys[1:] {
			ratio := median(cmpGroups[ck]) / baseline
			p0 := cmpGroups[ck][0]
			ck.kinds |= kindRatio
			ck.denom = cmpBase.key
			p0.Set(aesCompare, ck)
			p0.Set(aesRatio, value{kinds: kindContinuous | kindRatio, val: ratio})
			out = append(out, p0)
		}
	}

	return out, nil
}
