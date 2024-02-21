// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plot

import "golang.org/x/perf/benchproc"

type Config struct {
	aes aesMap[projection]

	logScale aesMap[int]
}

func NewConfig() *Config {
	return &Config{}
}

// SetIV maps independent variable iv to aesthetic aes.
func (c *Config) SetIV(aes Aes, iv *benchproc.Projection) {
	fields := iv.Fields()
	var ivField *benchproc.Field
	if len(fields) == 1 && !fields[0].IsTuple {
		ivField = fields[0]
	}
	var unitField *benchproc.Field
	for _, field := range fields {
		if field.Name == ".unit" {
			unitField = field
		}
	}
	c.aes.Set(aes, projection{iv: iv, ivField: ivField, unitField: unitField})
}

// SetDV maps the dependent variable to aesthetic aes.
func (c *Config) SetDV(aes Aes) {
	c.aes.Set(aes, projection{dv: true})
}

// SetLogScale sets the aesthetic dimension aes to use a log scale in the given
// base. Base 0 represents a linear scale.
func (c *Config) SetLogScale(aes Aes, base int) {
	c.logScale.Set(aes, base)
}
