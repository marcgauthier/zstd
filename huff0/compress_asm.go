//go:build (amd64 || arm64) && !noasm && gc

package huff0

import "slices"

type compress1xDoCtx struct {
	c      uint64
	nb     uint64
	pos    int64
	src    *byte
	buf    *byte
	ct     *cTableEntry
	startN int64
	count  int64
}

//go:noescape
func countSimple4_asm(src *byte, n int64, cnt0, cnt1, cnt2, cnt3 *uint32)

func compress1xDoLoop(w bitState, buf, src []byte, cTable []cTableEntry, n int) (bitState, []byte) {
	if n < 0 {
		return w, buf
	}
	const bytesPer4 = (7 + 4*tableLogMax) / 8
	groups := n/4 + 1
	need := groups*bytesPer4 + 8
	if len(buf)-w.pos < need {
		buf = slices.Grow(buf[:w.pos], need)
		buf = buf[:cap(buf)]
	}
	ctx := compress1xDoCtx{
		c:      w.c,
		nb:     uint64(w.nb),
		pos:    int64(w.pos),
		src:    &src[0],
		buf:    &buf[0],
		ct:     &cTable[0],
		startN: int64(n),
		count:  int64(groups),
	}
	compress1xDoAsm(&ctx)
	w.c = ctx.c
	w.nb = uint(ctx.nb)
	w.pos = int(ctx.pos)
	return w, buf
}

func countSimpleHist(in []byte, cnt *[256]uint32) {
	if len(in) < 512 {
		for _, v := range in {
			cnt[v]++
		}
		return
	}
	var c1, c2, c3 [256]uint32
	countSimple4_asm(&in[0], int64(len(in)), &cnt[0], &c1[0], &c2[0], &c3[0])
	for i := range 256 {
		cnt[i] += c1[i] + c2[i] + c3[i]
	}
}
