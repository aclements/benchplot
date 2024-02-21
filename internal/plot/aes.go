// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plot

import (
	"fmt"
	"strings"
	"sync"
)

// Aes is the name of an aesthetic.
type Aes int

const (
	AesX Aes = iota
	AesY
	AesColor
	AesRow // Facet row
	AesCol // Facet column

	aesMax

	aesNone = Aes(-1)
)

// Name returns a short name for aesthetic a, such as "x".
func (a Aes) Name() string {
	switch a {
	case AesX:
		return "x"
	case AesY:
		return "y"
	case AesColor:
		return "color"
	case AesRow:
		return "row"
	case AesCol:
		return "col"
	}
	return fmt.Sprintf("Aes(%d)", a)
}

var nameToAes = sync.OnceValue(func() map[string]Aes {
	m := make(map[string]Aes)
	for i := Aes(0); i < aesMax; i++ {
		m[i.Name()] = i
	}
	return m
})

// AesFromName is the inverse of [Aes.Name].
func AesFromName(name string) (Aes, bool) {
	aes, ok := nameToAes()[name]
	return aes, ok
}

// aesMap is an efficient map from Aes to T.
type aesMap[T any] struct {
	aes [aesMax]T
}

func (m *aesMap[T]) Set(aes Aes, val T) {
	m.aes[aes] = val
}

func (m *aesMap[T]) Get(aes Aes) T {
	return m.aes[aes]
}

func (m *aesMap[T]) Copy() aesMap[T] {
	return *m
}

func (m *aesMap[T]) String() string {
	var buf strings.Builder
	buf.WriteByte('{')
	for aes := range aesMax {
		if aes > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(aes.Name())
		buf.WriteByte(':')
		fmt.Fprint(&buf, m.Get(aes))
	}
	buf.WriteByte('}')
	return buf.String()
}

// transformAesMap sets the aesthetics in dst by transforming each value in src
// using transform.
func transformAesMap[S, D any](src *aesMap[S], dst *aesMap[D], transform func(S) D) {
	for i := range src.aes {
		dst.aes[i] = transform(src.aes[Aes(i)])
	}
}
