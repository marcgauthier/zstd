//go:build (amd64 || arm64) && !noasm && gc

package zstd

import "slices"

const useMatchAsm = !debugEncoder && !debugSequences && !debugMatches && !debugAsserts

type dfastEncodeCtx struct {
	src         *byte
	srcLen      int64
	s           int64
	sLimit      int64
	cur         int64
	maxMatchOff int64
	table       *tableEntry
	longTable   *tableEntry
	lits        *byte
	seqs        *seq
	litsLen     int64
	seqsLen     int64
	offset1     int64
	offset2     int64
	nextEmit    int64
}

type fastEncodeCtx struct {
	src         *byte
	srcLen      int64
	s           int64
	sLimit      int64
	cur         int64
	maxMatchOff int64
	table       *tableEntry
	lits        *byte
	seqs        *seq
	litsLen     int64
	seqsLen     int64
	offset1     int64
	offset2     int64
	nextEmit    int64
}

type betterEncodeCtx struct {
	src         *byte
	srcLen      int64
	s           int64
	sLimit      int64
	cur         int64
	maxMatchOff int64
	table       *tableEntry
	longTable   *prevEntry
	lits        *byte
	seqs        *seq
	litsLen     int64
	seqsLen     int64
	offset1     int64
	offset2     int64
	nextEmit    int64
}

type bestEncodeCtx struct {
	src            *byte
	srcLen         int64
	s              int64
	sLimit         int64
	cur            int64
	maxMatchOff    int64
	bitsPerByte    int64
	table          *prevEntry
	longTable      *prevEntry
	lits           *byte
	seqs           *seq
	litsLen        int64
	seqsLen        int64
	offset1        int64
	offset2        int64
	offset3        int64
	nextEmit       int64
	predefOF       *int32
	predefMLDirect *int32
	predefMLCost   *int32
}

//go:noescape
func doubleFastEncode_asm(ctx *dfastEncodeCtx)

//go:noescape
func doubleFrameEncode_asm(ctx *dfastEncodeCtx)

//go:noescape
func doublePackedFrameEncode_asm(ctx *dfastEncodeCtx)

//go:noescape
func fastEncode_asm(ctx *fastEncodeCtx)

//go:noescape
func betterFastEncode_asm(ctx *betterEncodeCtx)

//go:noescape
func bestFastEncode_asm(ctx *bestEncodeCtx)

func ensureMatchBufs(blk *blockEnc, blockLen int) {
	needLits := blockLen + 16
	if cap(blk.literals)-len(blk.literals) < needLits {
		blk.literals = slices.Grow(blk.literals, needLits)
	}
	needSeqs := blockLen/4 + 4
	if cap(blk.sequences)-len(blk.sequences) < needSeqs {
		blk.sequences = slices.Grow(blk.sequences, needSeqs)
	}
}

func doubleFastEncodeAsm(e *doubleFastEncoder, blk *blockEnc, src []byte, s, sLimit int32, saveOffsets bool) {
	ensureMatchBufs(blk, len(src)-int(s))
	litsCap := blk.literals[:cap(blk.literals)]
	seqsCap := blk.sequences[:cap(blk.sequences)]

	ctx := dfastEncodeCtx{
		src:         &src[0],
		srcLen:      int64(len(src)),
		s:           int64(s),
		sLimit:      int64(sLimit),
		cur:         int64(e.cur),
		maxMatchOff: int64(e.maxMatchOff),
		table:       &e.table[0],
		longTable:   &e.longTable[0],
		lits:        &litsCap[0],
		seqs:        &seqsCap[0],
		litsLen:     int64(len(blk.literals)),
		seqsLen:     int64(len(blk.sequences)),
		offset1:     int64(blk.recentOffsets[0]),
		offset2:     int64(blk.recentOffsets[1]),
	}
	if e.packedFrame {
		doublePackedFrameEncode_asm(&ctx)
	} else if e.fixedBias {
		doubleFrameEncode_asm(&ctx)
	} else {
		doubleFastEncode_asm(&ctx)
	}

	blk.literals = litsCap[:ctx.litsLen]
	blk.sequences = seqsCap[:ctx.seqsLen]
	if int(ctx.nextEmit) < len(src) {
		blk.literals = append(blk.literals, src[ctx.nextEmit:]...)
		blk.extraLits = len(src) - int(ctx.nextEmit)
	}
	if saveOffsets {
		blk.recentOffsets[0] = uint32(ctx.offset1)
		blk.recentOffsets[1] = uint32(ctx.offset2)
	}
}

func fastEncodeAsm(e *fastEncoder, blk *blockEnc, src []byte, s, sLimit int32, saveOffsets bool) {
	ensureMatchBufs(blk, len(src)-int(s))
	litsCap := blk.literals[:cap(blk.literals)]
	seqsCap := blk.sequences[:cap(blk.sequences)]

	ctx := fastEncodeCtx{
		src:         &src[0],
		srcLen:      int64(len(src)),
		s:           int64(s),
		sLimit:      int64(sLimit),
		cur:         int64(e.cur),
		maxMatchOff: int64(e.maxMatchOff),
		table:       &e.table[0],
		lits:        &litsCap[0],
		seqs:        &seqsCap[0],
		litsLen:     int64(len(blk.literals)),
		seqsLen:     int64(len(blk.sequences)),
		offset1:     int64(blk.recentOffsets[0]),
		offset2:     int64(blk.recentOffsets[1]),
	}
	fastEncode_asm(&ctx)

	blk.literals = litsCap[:ctx.litsLen]
	blk.sequences = seqsCap[:ctx.seqsLen]
	if int(ctx.nextEmit) < len(src) {
		blk.literals = append(blk.literals, src[ctx.nextEmit:]...)
		blk.extraLits = len(src) - int(ctx.nextEmit)
	}
	if saveOffsets {
		blk.recentOffsets[0] = uint32(ctx.offset1)
		blk.recentOffsets[1] = uint32(ctx.offset2)
	}
}

func betterFastEncodeAsm(e *betterFastEncoder, blk *blockEnc, src []byte, s, sLimit int32, saveOffsets bool) {
	ensureMatchBufs(blk, len(src)-int(s))
	litsCap := blk.literals[:cap(blk.literals)]
	seqsCap := blk.sequences[:cap(blk.sequences)]

	ctx := betterEncodeCtx{
		src:         &src[0],
		srcLen:      int64(len(src)),
		s:           int64(s),
		sLimit:      int64(sLimit),
		cur:         int64(e.cur),
		maxMatchOff: int64(e.maxMatchOff),
		table:       &e.table[0],
		longTable:   &e.longTable[0],
		lits:        &litsCap[0],
		seqs:        &seqsCap[0],
		litsLen:     int64(len(blk.literals)),
		seqsLen:     int64(len(blk.sequences)),
		offset1:     int64(blk.recentOffsets[0]),
		offset2:     int64(blk.recentOffsets[1]),
	}
	betterFastEncode_asm(&ctx)

	blk.literals = litsCap[:ctx.litsLen]
	blk.sequences = seqsCap[:ctx.seqsLen]
	if int(ctx.nextEmit) < len(src) {
		blk.literals = append(blk.literals, src[ctx.nextEmit:]...)
		blk.extraLits = len(src) - int(ctx.nextEmit)
	}
	if saveOffsets {
		blk.recentOffsets[0] = uint32(ctx.offset1)
		blk.recentOffsets[1] = uint32(ctx.offset2)
	}
}

func bestFastEncodeAsm(e *bestFastEncoder, blk *blockEnc, src []byte, s, sLimit, bitsPerByte int32, saveOffsets bool) {
	initPredefined()
	ensureMatchBufs(blk, len(src)-int(s))
	litsCap := blk.literals[:cap(blk.literals)]
	seqsCap := blk.sequences[:cap(blk.sequences)]

	ctx := bestEncodeCtx{
		src:            &src[0],
		srcLen:         int64(len(src)),
		s:              int64(s),
		sLimit:         int64(sLimit),
		cur:            int64(e.cur),
		maxMatchOff:    int64(e.maxMatchOff),
		bitsPerByte:    int64(bitsPerByte),
		table:          &e.table[0],
		longTable:      &e.longTable[0],
		lits:           &litsCap[0],
		seqs:           &seqsCap[0],
		litsLen:        int64(len(blk.literals)),
		seqsLen:        int64(len(blk.sequences)),
		offset1:        int64(blk.recentOffsets[0]),
		offset2:        int64(blk.recentOffsets[1]),
		offset3:        int64(blk.recentOffsets[2]),
		predefOF:       &predefOFCost[0],
		predefMLDirect: &predefMLDirect[0],
		predefMLCost:   &predefMLCost[0],
	}
	bestFastEncode_asm(&ctx)

	blk.literals = litsCap[:ctx.litsLen]
	blk.sequences = seqsCap[:ctx.seqsLen]
	if int(ctx.nextEmit) < len(src) {
		blk.literals = append(blk.literals, src[ctx.nextEmit:]...)
		blk.extraLits = len(src) - int(ctx.nextEmit)
	}
	if saveOffsets {
		blk.recentOffsets[0] = uint32(ctx.offset1)
		blk.recentOffsets[1] = uint32(ctx.offset2)
		blk.recentOffsets[2] = uint32(ctx.offset3)
	}
}
