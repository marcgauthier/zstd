//go:build (!amd64 && !arm64) || noasm || !gc

package zstd

import "slices"

func encodeSeqsLoop(
	w bitState,
	buf []byte,
	seqs []seq,
	seqIdx int,
	ofState, mlState, llState uint16,
	ofTT, mlTT, llTT []symbolTransform,
	ofTab, mlTab, llTab []uint16,
) (bitState, []byte, uint16, uint16, uint16) {
	const seqMaxBytes = 12 // 26 state bits + 63 extra bits.
	for seqIdx >= 0 {
		n := (len(buf) - w.pos - 16) / seqMaxBytes
		if n <= 0 {
			buf = slices.Grow(buf[:w.pos], (seqIdx+1)*seqMaxBytes+16)
			buf = buf[:cap(buf)]
			continue
		}
		for stop := max(seqIdx-n, -1); seqIdx > stop; seqIdx-- {
			s := seqs[seqIdx]
			ofB, mlB, llB := ofTT[s.ofCode], mlTT[s.mlCode], llTT[s.llCode]

			// w.nb <= 37 here, and the states add at most 26 bits.
			w, ofState = w.addState(ofState, ofTab, ofB)
			w, mlState = w.addState(mlState, mlTab, mlB)
			w, llState = w.addState(llState, llTab, llB)

			ofBits, mlBits, llBits := ofB.outBits&31, mlB.outBits&31, llB.outBits&31
			extra := uint64(s.offset&bitMask32[ofBits])<<(mlBits+llBits) |
				uint64(s.matchLen&bitMask32[mlBits])<<llBits |
				uint64(s.litLen&bitMask32[llBits])
			extraN := uint(ofBits + mlBits + llBits)
			if w.nb+extraN > 63 {
				w = w.flush(buf)
				if extraN > 56 {
					// Only offsets over 24 bits get here.
					low := uint(mlBits + llBits)
					w = w.add(extra&(1<<low-1), low).flush(buf)
					extra >>= low
					extraN -= low
				}
			}
			w = w.add(extra, extraN).flush(buf)
		}
	}
	return w, buf, ofState, mlState, llState
}

func genCodesSeqs(seqs []seq, llH, ofH, mlH *[256]uint32) {
	for i := range seqs {
		seq := &seqs[i]
		v := llCode(seq.litLen)
		seq.llCode = v
		llH[v]++

		v = ofCode(seq.offset)
		seq.ofCode = v
		ofH[v]++

		v = mlCode(seq.matchLen)
		seq.mlCode = v
		mlH[v]++
	}
}
