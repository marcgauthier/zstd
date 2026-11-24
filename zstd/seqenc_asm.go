//go:build (amd64 || arm64) && !noasm && gc

package zstd

import "slices"

type encodeSeqsCtx struct {
	c        uint64
	nb       uint64
	pos      int64
	ofState  uint64
	mlState  uint64
	llState  uint64
	seqs     *seq
	buf      *byte
	ofTT     *symbolTransform
	mlTT     *symbolTransform
	llTT     *symbolTransform
	ofTab    *uint16
	mlTab    *uint16
	llTab    *uint16
	startIdx int64
	count    int64
}

type genCodesCtx struct {
	seqs    *seq
	count   int64
	llH     *uint32
	ofH     *uint32
	mlH     *uint32
	llTable *byte
	mlTable *byte
}

//go:noescape
func genCodes_asm(ctx *genCodesCtx)

func encodeSeqsLoop(
	w bitState,
	buf []byte,
	seqs []seq,
	seqIdx int,
	ofState, mlState, llState uint16,
	ofTT, mlTT, llTT []symbolTransform,
	ofTab, mlTab, llTab []uint16,
) (bitState, []byte, uint16, uint16, uint16) {
	if seqIdx < 0 {
		return w, buf, ofState, mlState, llState
	}
	const seqMaxBytes = 12 // 26 state bits + 63 extra bits.
	need := (seqIdx+1)*seqMaxBytes + 16
	if len(buf)-w.pos < need {
		buf = slices.Grow(buf[:w.pos], need)
		buf = buf[:cap(buf)]
	}
	ctx := encodeSeqsCtx{
		c:        w.c,
		nb:       uint64(w.nb),
		pos:      int64(w.pos),
		ofState:  uint64(ofState),
		mlState:  uint64(mlState),
		llState:  uint64(llState),
		seqs:     &seqs[0],
		buf:      &buf[0],
		ofTT:     &ofTT[0],
		mlTT:     &mlTT[0],
		llTT:     &llTT[0],
		ofTab:    &ofTab[0],
		mlTab:    &mlTab[0],
		llTab:    &llTab[0],
		startIdx: int64(seqIdx),
		count:    int64(seqIdx + 1),
	}
	encodeSeqsAsm(&ctx)
	w.c = ctx.c
	w.nb = uint(ctx.nb)
	w.pos = int(ctx.pos)
	return w, buf, uint16(ctx.ofState), uint16(ctx.mlState), uint16(ctx.llState)
}

func genCodesSeqs(seqs []seq, llH, ofH, mlH *[256]uint32) {
	ctx := genCodesCtx{
		seqs:    &seqs[0],
		count:   int64(len(seqs)),
		llH:     &llH[0],
		ofH:     &ofH[0],
		mlH:     &mlH[0],
		llTable: &llCodeTable[0],
		mlTable: &mlCodeTable[0],
	}
	genCodes_asm(&ctx)
}
