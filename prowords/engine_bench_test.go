package prowords

import (
	"strings"
	"testing"
)

// BenchmarkFind measures the engine on a message far longer than a real
// one, where the cost of checking each candidate match against the ones
// already claimed shows up.
func BenchmarkFind(b *testing.B) {
	text := strings.Repeat("Need 25 cots at 214 Kaczmarek Street, call Diego M. Marchetti on 408-555-1212 "+
		"or diego@xanadu-city.org; see https://xanadu-city.org/ShelterStatus for [220V] gear. ", 20)
	b.ReportAllocs()
	for b.Loop() {
		Find(text)
	}
}
