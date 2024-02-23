// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plot

import (
	"fmt"

	"golang.org/x/perf/benchmath"
)

// transformSummarize groups points that differ only in aes and produces a
// single point for each group where aes is set to a summary of the group.
//
// aes must have kind kindContinuous.
func transformSummarize(pts []point, aes Aes, confidence float64) ([]point, error) {
	if pointsKinds(pts, aes)&kindContinuous == 0 {
		return nil, fmt.Errorf("%s data must be numeric", aes.Name())
	}

	groups, keys := groupBy(pts, func(pt point) point {
		pt.Set(aes, value{})
		return pt
	})

	// Compute summary for each group. We put this in a slice to avoid
	// allocating each Summary separately.
	var ys []float64 // Scratch slice
	summaries := make([]benchmath.Summary, len(keys))
	for i, k := range keys {
		ys = ys[:0]
		for _, pt := range groups[k] {
			ys = append(ys, pt.Get(aes).val)
		}
		sample := benchmath.NewSample(ys, &benchmath.DefaultThresholds)
		summaries[i] = benchmath.AssumeNothing.Summary(sample, confidence)
	}

	// Construct new points.
	out := make([]point, len(keys))
	for i, k := range keys {
		pt := groups[k][0]
		summary := &summaries[i]
		v := value{kinds: kindContinuous | kindSummary, val: summary.Center, summary: summary}
		pt.Set(aes, v)
		out[i] = pt
	}

	return out, nil
}
