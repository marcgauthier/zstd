package main

//go:generate go run gen_enc.go -out ../seqenc.s -arch amd64,arm64 -arm64-promote-stack-slots -pkg=zstd

import (
	"flag"

	_ "github.com/klauspost/compress"

	. "github.com/honeycombio/avo/build"
	. "github.com/honeycombio/avo/operand"
	"github.com/honeycombio/avo/reg"
)

func main() {
	flag.Parse()

	ConstraintExpr("!noasm,gc")

	genEncodeSeqs("encodeSeqs_amd64", false)
	genEncodeSeqs("encodeSeqs_bmi2", true)
	genGenCodes("genCodes_asm")

	Generate()
}

func genEncodeSeqs(name string, bmi2 bool) {
	Package("github.com/klauspost/compress/zstd")
	TEXT(name, 0, "func(ctx *encodeSeqsCtx)")
	Doc(name+" encodes zstd sequences in reverse into a bit buffer.", "")
	Pragma("noescape")

	ctx := Dereference(Param("ctx"))

	// Persistent loop registers (8 GPRs).
	c := GP64()
	nb := GP64()
	dstPtr := GP64()
	seqPtr := GP64()
	count := GP64()
	ofState := GP64()
	mlState := GP64()
	llState := GP64()

	// Stack slots for read-only pointers.
	// arm64 lowering promotes these to upper registers.
	ofTTSlot := AllocLocal(8)
	mlTTSlot := AllocLocal(8)
	llTTSlot := AllocLocal(8)
	ofTabSlot := AllocLocal(8)
	mlTabSlot := AllocLocal(8)
	llTabSlot := AllocLocal(8)
	dstBaseSlot := AllocLocal(8)
	var bitsPackSlot Mem
	if !bmi2 {
		bitsPackSlot = AllocLocal(8)
	}

	// Load initial state.
	Load(ctx.Field("c"), c)
	Load(ctx.Field("nb"), nb)
	Load(ctx.Field("ofState"), ofState)
	Load(ctx.Field("mlState"), mlState)
	Load(ctx.Field("llState"), llState)
	Load(ctx.Field("count"), count)

	// dstBase and dstPtr = buf + pos
	tmp := GP64()
	Load(ctx.Field("buf"), dstPtr)
	MOVQ(dstPtr, dstBaseSlot)
	Load(ctx.Field("pos"), tmp)
	ADDQ(tmp, dstPtr)

	// seqPtr = seqs + startIdx*16
	Load(ctx.Field("seqs"), seqPtr)
	Load(ctx.Field("startIdx"), tmp)
	SHLQ(Imm(4), tmp)
	ADDQ(tmp, seqPtr)

	// Save table pointers to local slots.
	Load(ctx.Field("ofTT"), tmp)
	MOVQ(tmp, ofTTSlot)
	Load(ctx.Field("mlTT"), tmp)
	MOVQ(tmp, mlTTSlot)
	Load(ctx.Field("llTT"), tmp)
	MOVQ(tmp, llTTSlot)
	Load(ctx.Field("ofTab"), tmp)
	MOVQ(tmp, ofTabSlot)
	Load(ctx.Field("mlTab"), tmp)
	MOVQ(tmp, mlTabSlot)
	Load(ctx.Field("llTab"), tmp)
	MOVQ(tmp, llTabSlot)

	// Helper: emit one FSE state transition + bit output, returning outBits&31 in outReg.
	emitState := func(codeDisp int, state, outReg reg.GPVirtual, ttSlot, tabSlot Mem) {
		ttAddr := GP64()
		t1 := GP64()
		t2 := GP64()

		MOVBQZX(Mem{Base: seqPtr, Disp: codeDisp}, t1)
		MOVQ(ttSlot, ttAddr)
		LEAQ(Mem{Base: ttAddr, Index: t1, Scale: 8}, ttAddr)

		// nbBitsOut = (uint32(state) + tt.deltaNbBits) >> 16
		MOVL(Mem{Base: ttAddr, Disp: 0}, t1.As32())
		ADDQ(state, t1)
		SHRQ(Imm(16), t1) // t1 = nbBitsOut

		if bmi2 {
			BZHIQ(t1, state, t2)
			SHRXQ(t1, state, state)
			SHLXQ(nb, t2, t2)
			ADDQ(t1, nb)
			ORQ(t2, c)
		} else {
			MOVQ(state, t2)
			MOVQ(t1, reg.RCX)
			SHRQ(reg.CL, state)
			MOVQ(state, t1)
			SHLQ(reg.CL, t1)
			SUBQ(t1, t2) // t2 = state & ((1<<nbBitsOut)-1)
			MOVQ(nb, t1)
			ADDQ(reg.RCX, nb)
			MOVQ(t1, reg.RCX)
			SHLQ(reg.CL, t2)
			ORQ(t2, c)
		}

		// nextState = tab[(state >> nbBitsOut) + int32(tt.deltaFindState)]
		MOVWQSX(Mem{Base: ttAddr, Disp: 4}, t1)
		ADDQ(t1, state)
		MOVQ(tabSlot, t2)
		MOVWQZX(Mem{Base: t2, Index: state, Scale: 2}, state)

		// Return outBits & 31 in register.
		MOVBQZX(Mem{Base: ttAddr, Disp: 6}, outReg)
		ANDQ(Imm(31), outReg)
	}

	// Helper: mask low b bits (0..31) of val.
	maskBits := func(val, b reg.GPVirtual) {
		if bmi2 {
			BZHIQ(b, val, val)
		} else {
			m := GP64()
			MOVQ(U64(1), m)
			MOVQ(b, reg.RCX)
			SHLQ(reg.CL, m)
			DECQ(m)
			ANDQ(m, val)
		}
	}

	// Helper: flush whole bytes of c to dstPtr (requires nb <= 63).
	emitFlush := func() {
		MOVQ(c, Mem{Base: dstPtr})
		t := GP64()
		MOVQ(nb, t)
		SHRQ(Imm(3), t)
		ADDQ(t, dstPtr)
		if bmi2 {
			SHLQ(Imm(3), t)
			SHRXQ(t, c, c)
		} else {
			MOVQ(nb, reg.RCX)
			ANDQ(Imm(0x38), reg.RCX)
			SHRQ(reg.CL, c)
		}
		ANDQ(Imm(7), nb)
	}

	PCALIGN(Imm(32))
	Label("loop")

	// 1. Encode FSE states in order: of, ml, ll.
	// Pack ofBits (low byte) and mlBits (byte 1) into bitsPack (reg on bmi2, 1 slot on non-bmi2).
	var bitsPack reg.GPVirtual
	extraN := GP64()
	if bmi2 {
		bitsPack = GP64()
		emitState(14, ofState, bitsPack, ofTTSlot, ofTabSlot)
		mlB := GP64()
		emitState(13, mlState, mlB, mlTTSlot, mlTabSlot)
		SHLQ(Imm(8), mlB)
		ORQ(mlB, bitsPack)
		emitState(12, llState, extraN, llTTSlot, llTabSlot)
	} else {
		bp := GP64()
		emitState(14, ofState, bp, ofTTSlot, ofTabSlot)
		MOVQ(bp, bitsPackSlot)
		emitState(13, mlState, bp, mlTTSlot, mlTabSlot)
		SHLQ(Imm(8), bp)
		ORQ(bitsPackSlot, bp)
		MOVQ(bp, bitsPackSlot)
		emitState(12, llState, extraN, llTTSlot, llTabSlot)
	}

	// 2. Pack extra bits: litLen | (matchLen << llBits) | (offset << (llBits + mlBits)).
	extra := GP64()
	v := GP64()
	b := GP64()

	// litLen (extraN holds llBits)
	MOVL(Mem{Base: seqPtr, Disp: 0}, extra.As32())
	maskBits(extra, extraN)

	// matchLen (bitsPack >> 8 holds mlBits)
	MOVL(Mem{Base: seqPtr, Disp: 4}, v.As32())
	if bmi2 {
		MOVQ(bitsPack, b)
	} else {
		MOVQ(bitsPackSlot, b)
	}
	SHRQ(Imm(8), b)
	maskBits(v, b)
	if bmi2 {
		SHLXQ(extraN, v, v)
	} else {
		MOVQ(extraN, reg.RCX)
		SHLQ(reg.CL, v)
	}
	ORQ(v, extra)
	ADDQ(b, extraN)

	// offset (bitsPack & 0xff holds ofBits)
	MOVL(Mem{Base: seqPtr, Disp: 8}, v.As32())
	if bmi2 {
		ANDQ(Imm(0xff), bitsPack)
		maskBits(v, bitsPack)
		SHLXQ(extraN, v, v)
		ORQ(v, extra)
		// Save llBits+mlBits in b before adding ofBits in case rare >56 path triggers.
		MOVQ(extraN, b)
		ADDQ(bitsPack, extraN)
	} else {
		MOVQ(bitsPackSlot, b)
		ANDQ(Imm(0xff), b)
		maskBits(v, b)
		MOVQ(extraN, reg.RCX)
		SHLQ(reg.CL, v)
		ORQ(v, extra)
		// Save llBits+mlBits to bitsPackSlot before adding ofBits.
		MOVQ(extraN, bitsPackSlot)
		ADDQ(b, extraN)
	}

	// 3. Check if nb + extraN > 63.
	LEAQ(Mem{Base: nb, Index: extraN, Scale: 1}, v)
	CMPQ(v, Imm(63))
	JBE(LabelRef("add_extra"))

	// Pre-flush to make room.
	emitFlush()

	// Rare path: extraN > 56 (offsets > 24 bits).
	CMPQ(extraN, Imm(56))
	JBE(LabelRef("add_extra"))
	{
		if !bmi2 {
			MOVQ(bitsPackSlot, b)
		}
		// b holds llBits + mlBits (<= 32).
		lowVal := GP64()
		if bmi2 {
			BZHIQ(b, extra, lowVal)
		} else {
			MOVQ(U64(1), lowVal)
			MOVQ(b, reg.RCX)
			SHLQ(reg.CL, lowVal)
			DECQ(lowVal)
			ANDQ(extra, lowVal)
		}
		if bmi2 {
			SHLXQ(nb, lowVal, lowVal)
		} else {
			MOVQ(nb, reg.RCX)
			SHLQ(reg.CL, lowVal)
		}
		ORQ(lowVal, c)
		ADDQ(b, nb)
		emitFlush()

		if bmi2 {
			SHRXQ(b, extra, extra)
		} else {
			MOVQ(b, reg.RCX)
			SHRQ(reg.CL, extra)
		}
		SUBQ(b, extraN)
	}

	Label("add_extra")
	if bmi2 {
		SHLXQ(nb, extra, extra)
	} else {
		MOVQ(nb, reg.RCX)
		SHLQ(reg.CL, extra)
	}
	ORQ(extra, c)
	ADDQ(extraN, nb)
	emitFlush()

	SUBQ(Imm(16), seqPtr)
	DECQ(count)
	JNZ(LabelRef("loop"))

	// Store updated state back to ctx.
	ctxPtr := Param("ctx").Base()
	// Reload ctx pointer via Store(val, ctx.Field(...)).
	_ = ctxPtr
	Store(c, ctx.Field("c"))
	Store(nb, ctx.Field("nb"))
	Store(ofState, ctx.Field("ofState"))
	Store(mlState, ctx.Field("mlState"))
	Store(llState, ctx.Field("llState"))
	MOVQ(dstBaseSlot, tmp)
	SUBQ(tmp, dstPtr)
	Store(dstPtr, ctx.Field("pos"))

	RET()
}

func genGenCodes(name string) {
	Package("github.com/klauspost/compress/zstd")
	TEXT(name, 0, "func(ctx *genCodesCtx)")
	Doc(name+" computes llCode, mlCode, ofCode for all sequences and builds their histograms.", "")
	Pragma("noescape")

	ctx := Dereference(Param("ctx"))

	seqPtr := GP64()
	count := GP64()
	llH := GP64()
	ofH := GP64()
	mlH := GP64()
	llTab := GP64()
	mlTab := GP64()

	Load(ctx.Field("seqs"), seqPtr)
	Load(ctx.Field("count"), count)
	Load(ctx.Field("llH"), llH)
	Load(ctx.Field("ofH"), ofH)
	Load(ctx.Field("mlH"), mlH)
	Load(ctx.Field("llTable"), llTab)
	Load(ctx.Field("mlTable"), mlTab)

	val := GP64()
	code := GP64()
	cnt := GP64()

	PCALIGN(Imm(32))
	Label("loop")

	// 1. llCode(seq.litLen)
	MOVL(Mem{Base: seqPtr, Disp: 0}, val.As32())
	CMPQ(val, Imm(63))
	JA(LabelRef("ll_big"))
	MOVBQZX(Mem{Base: llTab, Index: val, Scale: 1}, code)
	JMP(LabelRef("ll_done"))
	Label("ll_big")
	BSRQ(val, code)
	ADDQ(Imm(19), code)
	Label("ll_done")
	MOVB(code.As8(), Mem{Base: seqPtr, Disp: 12})
	MOVL(Mem{Base: llH, Index: code, Scale: 4}, cnt.As32())
	INCQ(cnt)
	MOVL(cnt.As32(), Mem{Base: llH, Index: code, Scale: 4})

	// 2. ofCode(seq.offset) -- offset is always >= 1
	MOVL(Mem{Base: seqPtr, Disp: 8}, val.As32())
	BSRQ(val, code)
	MOVB(code.As8(), Mem{Base: seqPtr, Disp: 14})
	MOVL(Mem{Base: ofH, Index: code, Scale: 4}, cnt.As32())
	INCQ(cnt)
	MOVL(cnt.As32(), Mem{Base: ofH, Index: code, Scale: 4})

	// 3. mlCode(seq.matchLen)
	MOVL(Mem{Base: seqPtr, Disp: 4}, val.As32())
	CMPQ(val, Imm(127))
	JA(LabelRef("ml_big"))
	MOVBQZX(Mem{Base: mlTab, Index: val, Scale: 1}, code)
	JMP(LabelRef("ml_done"))
	Label("ml_big")
	BSRQ(val, code)
	ADDQ(Imm(36), code)
	Label("ml_done")
	MOVB(code.As8(), Mem{Base: seqPtr, Disp: 13})
	MOVL(Mem{Base: mlH, Index: code, Scale: 4}, cnt.As32())
	INCQ(cnt)
	MOVL(cnt.As32(), Mem{Base: mlH, Index: code, Scale: 4})

	ADDQ(Imm(16), seqPtr)
	DECQ(count)
	JNZ(LabelRef("loop"))

	RET()
}
