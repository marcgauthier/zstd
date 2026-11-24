//go:build (!amd64 && !arm64) || noasm || !gc

package huff0

import "slices"

func compress1xDoLoop(w bitState, buf, src []byte, cTable []cTableEntry, n int) (bitState, []byte) {
	const bytesPer4 = (7 + 4*tableLogMax) / 8
	for n >= 0 {
		k := (len(buf) - w.pos - 8) / bytesPer4
		if k <= 0 {
			buf = slices.Grow(buf[:w.pos], (n/4+1)*bytesPer4+8)
			buf = buf[:cap(buf)]
			continue
		}
		for stop := max(n-4*k, -4); n > stop; n -= 4 {
			w = w.flush(buf)
			tmp := src[n : n+4]
			w = w.add(fourSymbols(cTable[tmp[3]], cTable[tmp[2]], cTable[tmp[1]], cTable[tmp[0]]))
		}
	}
	return w, buf
}

func countSimpleHist(in []byte, cnt *[256]uint32) {
	if len(in) >= 256 {
		var c1 [256]uint32
		for len(in) >= 4 {
			cnt[in[0]]++
			c1[in[1]]++
			cnt[in[2]]++
			c1[in[3]]++
			in = in[4:]
		}
		for _, v := range in {
			cnt[v]++
		}
		for i, v := range c1 {
			cnt[i] += v
		}
		return
	}
	for len(in) >= 4 {
		cnt[in[0]]++
		cnt[in[1]]++
		cnt[in[2]]++
		cnt[in[3]]++
		in = in[4:]
	}
	for _, v := range in {
		cnt[v]++
	}
}
