package main

//go:generate go run gen_match.go -out ../match.s -arch amd64,arm64 -arm64-promote-stack-slots -pkg=zstd

import (
	"flag"
	"fmt"

	_ "github.com/klauspost/compress"

	. "github.com/honeycombio/avo/build"
	. "github.com/honeycombio/avo/operand"
	"github.com/honeycombio/avo/reg"
)

const (
	prime4bytes    = uint64(2654435761)
	prime5bytes    = uint64(889523592379)
	prime6bytes    = uint64(227718039650203)
	prime8bytes    = uint64(0xcf1bbcdcb7a56463)
	maxMatchLength = 131074
	highScore      = maxMatchLength * 8
	zstdMinMatch   = 3
)

func main() {
	flag.Parse()

	ConstraintExpr("!noasm,gc")

	genDoubleFastEncode("doubleFastEncode_asm", doubleFastHistory)
	genDoubleFastEncode("doubleFrameEncode_asm", doubleFastFixedFrame)
	genDoubleFastEncode("doublePackedFrameEncode_asm", doubleFastPackedFrame)
	genFastEncode("fastEncode_asm")
	genBetterFastEncode("betterFastEncode_asm")
	genBestFastEncode("bestFastEncode_asm")

	Generate()
}

type matchHelpers struct {
	lblID int
}

func (h *matchHelpers) lbl(name string) string {
	h.lblID++
	return fmt.Sprintf("%s_%d", name, h.lblID)
}

// matchLen computes common prefix length of src[aOff:] and src[bOff:] into outLen.
// Uses direct indexed loads on the 1-shot fast path for minimal latency.
func (h *matchHelpers) matchLen(src, aOff, bOff, outLen reg.GPVirtual, srcLenSlot Mem) {
	rem := GP64()
	pA := GP64()
	pB := GP64()
	t := GP64()

	MOVQ(srcLenSlot, rem)
	SUBQ(aOff, rem)
	XORQ(outLen, outLen)

	lSlow := h.lbl("ml_slow")
	lLoop8 := h.lbl("ml_loop8")
	lLoop8Body := h.lbl("ml_loop8_body")
	lFound8 := h.lbl("ml_found8")
	lTail := h.lbl("ml_tail")
	lTail1 := h.lbl("ml_tail1")
	lDone := h.lbl("ml_done")

	CMPQ(rem, Imm(8))
	JB(LabelRef(lSlow))

	// Fast path: check first 8 bytes directly without building advancing pointers.
	MOVQ(Mem{Base: src, Index: aOff, Scale: 1}, t)
	XORQ(Mem{Base: src, Index: bOff, Scale: 1}, t)
	JZ(LabelRef(lLoop8))
	BSFQ(t, outLen)
	SHRQ(Imm(3), outLen)
	JMP(LabelRef(lDone))

	// 8+ bytes matched: advance pointers and loop 8 bytes at a time.
	Label(lLoop8)
	LEAQ(Mem{Base: src, Index: aOff, Scale: 1, Disp: 8}, pA)
	LEAQ(Mem{Base: src, Index: bOff, Scale: 1, Disp: 8}, pB)
	MOVQ(U64(8), outLen)
	SUBQ(Imm(8), rem)
	CMPQ(rem, Imm(8))
	JB(LabelRef(lTail))

	Label(lLoop8Body)
	MOVQ(Mem{Base: pA}, t)
	XORQ(Mem{Base: pB}, t)
	JNZ(LabelRef(lFound8))
	ADDQ(Imm(8), pA)
	ADDQ(Imm(8), pB)
	ADDQ(Imm(8), outLen)
	SUBQ(Imm(8), rem)
	CMPQ(rem, Imm(8))
	JAE(LabelRef(lLoop8Body))
	JMP(LabelRef(lTail))

	Label(lFound8)
	BSFQ(t, t)
	SHRQ(Imm(3), t)
	ADDQ(t, outLen)
	JMP(LabelRef(lDone))

	Label(lSlow)
	TESTQ(rem, rem)
	JZ(LabelRef(lDone))
	LEAQ(Mem{Base: src, Index: aOff, Scale: 1}, pA)
	LEAQ(Mem{Base: src, Index: bOff, Scale: 1}, pB)

	Label(lTail)
	TESTQ(rem, rem)
	JZ(LabelRef(lDone))

	t2 := GP64()
	Label(lTail1)
	MOVBQZX(Mem{Base: pA}, t)
	MOVBQZX(Mem{Base: pB}, t2)
	CMPQ(t, t2)
	JNE(LabelRef(lDone))
	INCQ(pA)
	INCQ(pB)
	INCQ(outLen)
	DECQ(rem)
	JNZ(LabelRef(lTail1))

	Label(lDone)
}

// matchLenPtr is a register-saving variant of matchLen that releases aOff/bOff immediately.
func (h *matchHelpers) matchLenPtr(src, aOff, bOff, outLen reg.GPVirtual, srcLenSlot Mem) {
	rem := GP64()
	pA := GP64()
	pB := GP64()
	t := GP64()

	LEAQ(Mem{Base: src, Index: aOff, Scale: 1}, pA)
	LEAQ(Mem{Base: src, Index: bOff, Scale: 1}, pB)
	MOVQ(srcLenSlot, rem)
	SUBQ(aOff, rem)
	XORQ(outLen, outLen)

	lLoop8 := h.lbl("mlp_loop8")
	lLoop8Body := h.lbl("mlp_loop8_body")
	lFound8 := h.lbl("mlp_found8")
	lTail := h.lbl("mlp_tail")
	lTail1 := h.lbl("mlp_tail1")
	lDone := h.lbl("mlp_done")

	CMPQ(rem, Imm(8))
	JB(LabelRef(lTail))

	MOVQ(Mem{Base: pA}, t)
	XORQ(Mem{Base: pB}, t)
	JZ(LabelRef(lLoop8))
	BSFQ(t, outLen)
	SHRQ(Imm(3), outLen)
	JMP(LabelRef(lDone))

	Label(lLoop8)
	ADDQ(Imm(8), pA)
	ADDQ(Imm(8), pB)
	MOVQ(U64(8), outLen)
	SUBQ(Imm(8), rem)
	CMPQ(rem, Imm(8))
	JB(LabelRef(lTail))

	Label(lLoop8Body)
	MOVQ(Mem{Base: pA}, t)
	XORQ(Mem{Base: pB}, t)
	JNZ(LabelRef(lFound8))
	ADDQ(Imm(8), pA)
	ADDQ(Imm(8), pB)
	ADDQ(Imm(8), outLen)
	SUBQ(Imm(8), rem)
	CMPQ(rem, Imm(8))
	JAE(LabelRef(lLoop8Body))
	JMP(LabelRef(lTail))

	Label(lFound8)
	BSFQ(t, t)
	SHRQ(Imm(3), t)
	ADDQ(t, outLen)
	JMP(LabelRef(lDone))

	Label(lTail)
	TESTQ(rem, rem)
	JZ(LabelRef(lDone))

	t2 := GP64()
	Label(lTail1)
	MOVBQZX(Mem{Base: pA}, t)
	MOVBQZX(Mem{Base: pB}, t2)
	CMPQ(t, t2)
	JNE(LabelRef(lDone))
	INCQ(pA)
	INCQ(pB)
	INCQ(outLen)
	DECQ(rem)
	JNZ(LabelRef(lTail1))

	Label(lDone)
}

// copyLits copies src[nextEmit:until] into litsPtrSlot and updates litsPtrSlot.
// Requires nextEmit <= until and (if litLen in 1..7) nextEmit + 8 <= len(src),
// and lits has at least 16 bytes of slack past the end.
// Returns litLen in outLitLen.
func (h *matchHelpers) copyLits(src, nextEmit, until, outLitLen reg.GPVirtual, litsPtrSlot Mem) {
	MOVQ(until, outLitLen)
	SUBQ(nextEmit, outLitLen)
	lDone := h.lbl("lits_done")
	JZ(LabelRef(lDone))

	dst := GP64()
	pSrc := GP64()
	t1 := GP64()
	MOVQ(litsPtrSlot, dst)
	LEAQ(Mem{Base: src, Index: nextEmit, Scale: 1}, pSrc)

	lGe8 := h.lbl("lits_ge8")
	lGe16 := h.lbl("lits_ge16")
	lLoop16 := h.lbl("lits_loop16")
	lTail16 := h.lbl("lits_tail16")

	CMPQ(outLitLen, Imm(8))
	JAE(LabelRef(lGe8))
	// 1..7 bytes: single 8-byte copy (safe since nextEmit+8 <= len(src) and dst has +16 slack).
	MOVQ(Mem{Base: pSrc}, t1)
	MOVQ(t1, Mem{Base: dst})
	ADDQ(outLitLen, dst)
	MOVQ(dst, litsPtrSlot)
	JMP(LabelRef(lDone))

	Label(lGe8)
	CMPQ(outLitLen, Imm(16))
	JAE(LabelRef(lGe16))
	// 8..15 bytes: two overlapping 8-byte copies.
	t2 := GP64()
	MOVQ(Mem{Base: pSrc}, t1)
	MOVQ(Mem{Base: pSrc, Index: outLitLen, Scale: 1, Disp: -8}, t2)
	MOVQ(t1, Mem{Base: dst})
	MOVQ(t2, Mem{Base: dst, Index: outLitLen, Scale: 1, Disp: -8})
	ADDQ(outLitLen, dst)
	MOVQ(dst, litsPtrSlot)
	JMP(LabelRef(lDone))

	Label(lGe16)
	// >= 16 bytes: 16-byte MOVUPS loop + overlapping 16-byte tail.
	x0 := XMM()
	rem := GP64()
	MOVQ(outLitLen, rem)
	SUBQ(Imm(16), rem)
	JZ(LabelRef(lTail16))

	Label(lLoop16)
	MOVUPS(Mem{Base: pSrc}, x0)
	MOVUPS(x0, Mem{Base: dst})
	ADDQ(Imm(16), pSrc)
	ADDQ(Imm(16), dst)
	SUBQ(Imm(16), rem)
	JG(LabelRef(lLoop16))

	// Final overlapping 16-byte copy at end.
	Label(lTail16)
	ADDQ(rem, pSrc)
	ADDQ(rem, dst)
	MOVUPS(Mem{Base: pSrc}, x0)
	MOVUPS(x0, Mem{Base: dst})
	ADDQ(Imm(16), dst)
	MOVQ(dst, litsPtrSlot)

	Label(lDone)
}

func packTableEntry(offPlusCur, val, out reg.GPVirtual) {
	t := GP64()
	MOVQ(val, out)
	SHLQ(Imm(32), out)
	SHRQ(Imm(32), out)
	MOVQ(offPlusCur, t)
	SHLQ(Imm(32), t)
	ORQ(t, out)
}

// packTableEntryConsumeLive computes (offPlusCur << 32) | uint32(val) into out,
// clobbering offPlusCur. Caller MUST keep val live after this call so avo cannot
// coalesce val and out (MOVL reg, reg would be elided and leave upper 32 bits dirty).
func packTableEntryConsumeLive(offPlusCur, val, out reg.GPVirtual) {
	MOVL(val.As32(), out.As32())
	SHLQ(Imm(32), offPlusCur)
	ORQ(offPlusCur, out)
}

func packPrevEntry(offPlusCur, prevCand, out reg.GPVirtual) {
	// prevEntry{offset: offPlusCur (low 32), prev: prevCand.offset (low 32 of prevCand -> high 32)}
	t := GP64()
	MOVQ(offPlusCur, out)
	SHLQ(Imm(32), out)
	SHRQ(Imm(32), out)
	MOVQ(prevCand, t)
	SHLQ(Imm(32), t)
	ORQ(t, out)
}

type doubleFastTableMode uint8

const (
	doubleFastHistory doubleFastTableMode = iota
	doubleFastFixedFrame
	doubleFastPackedFrame
)

func genDoubleFastEncode(name string, mode doubleFastTableMode) {
	Package("github.com/klauspost/compress/zstd")
	TEXT(name, 0, "func(ctx *dfastEncodeCtx)")
	Doc(name+" implements doubleFastEncoder.Encode inner loop with pipelined table prefetches.", "")
	Pragma("noescape")

	ctx := Dereference(Param("ctx"))
	var h matchHelpers

	fixedBias := mode == doubleFastFixedFrame
	packed := mode == doubleFastPackedFrame
	indexScale := uint8(8)
	if packed {
		indexScale = 4
	}

	// Persistent loop registers (7 GPRs: keep both table pointers in registers!).
	src := GP64()
	s := GP64()
	nextEmit := GP64()
	cv := GP64()
	offset1 := GP64()
	tab := GP64()
	longTab := GP64()

	// Local stack slots (promoted to extra regs on arm64).
	offset2Slot := AllocLocal(8)
	tCandSSlot := AllocLocal(8)
	var shortDiffSlot Mem
	if packed {
		shortDiffSlot = AllocLocal(8)
	}
	srcLenSlot := AllocLocal(8)
	sLimitSlot := AllocLocal(8)
	var curSlot, maxOffSlot Mem
	if !packed {
		curSlot = AllocLocal(8)
		maxOffSlot = AllocLocal(8)
	}
	litsBaseSlot := AllocLocal(8)
	litsPtrSlot := AllocLocal(8)
	seqsBaseSlot := AllocLocal(8)
	seqPtrSlot := AllocLocal(8)
	seqs3Slot := AllocLocal(8)
	seqs4Slot := AllocLocal(8)
	primeLSlot := AllocLocal(8)
	primeSSlot := AllocLocal(8)

	Load(ctx.Field("src"), src)
	Load(ctx.Field("s"), s)
	MOVQ(s, nextEmit)
	Load(ctx.Field("offset1"), offset1)
	Load(ctx.Field("table"), tab)
	Load(ctx.Field("longTable"), longTab)

	tmp := GP64()
	Load(ctx.Field("offset2"), tmp)
	MOVQ(tmp, offset2Slot)
	Load(ctx.Field("srcLen"), tmp)
	MOVQ(tmp, srcLenSlot)
	Load(ctx.Field("sLimit"), tmp)
	MOVQ(tmp, sLimitSlot)
	const bias = 1 << 29 // Cleared entries are outside every allowed window.
	var cur Op = curSlot
	if !packed {
		if fixedBias {
			cur = Imm(bias)
		} else {
			Load(ctx.Field("cur"), tmp)
			MOVQ(tmp, curSlot)
		}
		Load(ctx.Field("maxMatchOff"), tmp)
		MOVQ(tmp, maxOffSlot)
	}

	litsTmp := GP64()
	Load(ctx.Field("lits"), litsTmp)
	MOVQ(litsTmp, litsBaseSlot)
	Load(ctx.Field("litsLen"), tmp)
	ADDQ(tmp, litsTmp)
	MOVQ(litsTmp, litsPtrSlot)

	seqTmp := GP64()
	Load(ctx.Field("seqs"), seqTmp)
	MOVQ(seqTmp, seqsBaseSlot)
	LEAQ(Mem{Base: seqTmp, Disp: 32}, tmp)
	MOVQ(tmp, seqs3Slot)
	LEAQ(Mem{Base: seqTmp, Disp: 48}, tmp)
	MOVQ(tmp, seqs4Slot)
	Load(ctx.Field("seqsLen"), tmp)
	SHLQ(Imm(4), tmp)
	ADDQ(tmp, seqTmp)
	MOVQ(seqTmp, seqPtrSlot)

	MOVQ(U64(prime8bytes), tmp)
	MOVQ(tmp, primeLSlot)
	if packed {
		MOVQ(U64(prime5bytes<<24), tmp)
	} else {
		MOVQ(U64(prime5bytes), tmp)
	}
	MOVQ(tmp, primeSSlot)

	hashL := func(val, out reg.GPVirtual) {
		MOVQ(val, out)
		IMULQ(primeLSlot, out)
		SHRQ(Imm(47), out) // 64 - 17
	}
	hashS := func(val, out reg.GPVirtual) {
		MOVQ(val, out)
		if !packed {
			SHLQ(Imm(24), out) // 64 - 40
		}
		IMULQ(primeSSlot, out)
		SHRQ(Imm(49), out) // 64 - 15
	}

	// The upper 24 bits hold a nonzero position, and the low byte holds a tag.
	packEntry := func(pos, word reg.GPVirtual, disp int) reg.GPVirtual {
		entry := GP64()
		tag := GP64()
		LEAL(Mem{Base: pos, Disp: disp + 1}, entry.As32())
		SHLL(Imm(8), entry.As32())
		MOVBLZX(word.As8(), tag.As32())
		ORL(tag.As32(), entry.As32())
		return entry
	}
	storeEntry := func(pos, word, hash, table reg.GPVirtual, disp int) {
		entry := packEntry(pos, word, disp)
		MOVL(entry.As32(), Mem{Base: table, Index: hash, Scale: 4})
	}
	var diff reg.GPVirtual
	if packed {
		diff = GP64()
	}
	checkEntry := func(cand, word, pos reg.GPVirtual, reject string) {
		CMPB(word.As8(), cand.As8())
		JNE(LabelRef(reject))
		MOVL(cand.As32(), pos.As32())
		SHRL(Imm(8), pos.As32())
		DECQ(pos)
		JS(LabelRef(reject))
		MOVQ(Mem{Base: src, Index: pos, Scale: 1}, diff)
		XORQ(word, diff)
		TESTL(diff.As32(), diff.As32())
		JNE(LabelRef(reject))
	}

	// Normal match extension leaves the next search position unchanged.
	hintNext := func(length reg.GPVirtual, displacement int) {
		done := h.lbl("hint_next_done")
		next := GP64()
		LEAQ(Mem{Base: s, Index: length, Scale: 1, Disp: displacement}, next)
		CMPQ(next, sLimitSlot)
		JGE(LabelRef(done))
		word := GP64()
		MOVQ(Mem{Base: src, Index: next, Scale: 1}, word)
		hashL(word, word)
		PREFETCHT0(Mem{Base: longTab, Index: word, Scale: indexScale})
		Label(done)
	}

	MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)

	PCALIGN(Imm(32))
	Label("encode_loop")
	candL := GP64()
	candS := GP64()
	tReg := GP64()
	{
		hL := GP64()
		hS := GP64()
		hashL(cv, hL)
		hashS(cv, hS)

		if packed {
			// Load both candidates back-to-back for 2-way MLP.
			MOVL(Mem{Base: longTab, Index: hL, Scale: 4}, candL.As32())
			MOVL(Mem{Base: tab, Index: hS, Scale: 4}, candS.As32())

			entry := packEntry(s, cv, 0)
			MOVL(entry.As32(), Mem{Base: longTab, Index: hL, Scale: 4})
			MOVL(entry.As32(), Mem{Base: tab, Index: hS, Scale: 4})
		} else {
			// Load both candidates back-to-back for 2-way MLP.
			MOVQ(Mem{Base: longTab, Index: hL, Scale: 8}, candL)
			MOVQ(Mem{Base: tab, Index: hS, Scale: 8}, candS)

			// Store updated entry into both tables.
			sCur := GP64()
			if fixedBias {
				LEAQ(Mem{Base: s, Disp: bias}, sCur)
			} else {
				MOVQ(s, sCur)
				ADDQ(cur, sCur)
			}
			entry := GP64()
			packTableEntryConsumeLive(sCur, cv, entry)
			MOVQ(entry, Mem{Base: longTab, Index: hL, Scale: 8})
			MOVQ(entry, Mem{Base: tab, Index: hS, Scale: 8})
		}
	}

	// Check repeat offset 1 at s+1 if len(sequences) > 2.
	{
		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		CMPQ(sp, seqs3Slot)
		JBE(LabelRef("check_long"))
	}
	{
		repIdx := GP64()
		MOVQ(s, repIdx)
		SUBQ(offset1, repIdx)
		INCQ(repIdx)
		TESTQ(repIdx, repIdx)
		JS(LabelRef("check_long"))

		cvShifted := GP64()
		MOVQ(cv, cvShifted)
		SHRQ(Imm(8), cvShifted)
		MOVL(Mem{Base: src, Index: repIdx, Scale: 1}, tReg.As32())
		CMPL(tReg.As32(), cvShifted.As32())
		JNE(LabelRef("check_long"))

		aOff := GP64()
		bOff := GP64()
		LEAQ(Mem{Base: s, Disp: 5}, aOff)
		LEAQ(Mem{Base: repIdx, Disp: 4}, bOff)
		origLen := GP64()
		h.matchLen(src, aOff, bOff, origLen, srcLenSlot)
		ADDQ(Imm(4), origLen)
		hintNext(origLen, 1)

		ml := GP64()
		LEAQ(Mem{Base: origLen, Disp: -zstdMinMatch}, ml)

		start := GP64()
		LEAQ(Mem{Base: s, Disp: 1}, start)
		startLim := GP64()
		LEAQ(Mem{Base: nextEmit, Disp: 1}, startLim)
		var tMin Op = Imm(0)
		if !packed {
			minPos := GP64()
			MOVQ(s, minPos)
			SUBQ(maxOffSlot, minPos)
			JNS(LabelRef("rep_tmin_ok"))
			XORQ(minPos, minPos)
			Label("rep_tmin_ok")
			tMin = minPos
		}

		Label("rep_back_loop")
		CMPQ(repIdx, tMin)
		JLE(LabelRef("rep_back_done"))
		CMPQ(start, startLim)
		JLE(LabelRef("rep_back_done"))
		CMPQ(ml, Imm(maxMatchLength-zstdMinMatch-1))
		JAE(LabelRef("rep_back_done"))
		b1 := GP64()
		b2 := GP64()
		MOVBQZX(Mem{Base: src, Index: repIdx, Scale: 1, Disp: -1}, b1)
		MOVBQZX(Mem{Base: src, Index: start, Scale: 1, Disp: -1}, b2)
		CMPQ(b1, b2)
		JNE(LabelRef("rep_back_done"))
		DECQ(repIdx)
		DECQ(start)
		INCQ(ml)
		JMP(LabelRef("rep_back_loop"))

		Label("rep_back_done")
		litLen := GP64()
		h.copyLits(src, nextEmit, start, litLen, litsPtrSlot)

		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		MOVL(litLen.As32(), Mem{Base: sp, Disp: 0})
		MOVL(ml.As32(), Mem{Base: sp, Disp: 4})
		MOVL(U32(1), Mem{Base: sp, Disp: 8})
		ADDQ(Imm(16), sp)
		MOVQ(sp, seqPtrSlot)

		LEAQ(Mem{Base: s, Index: origLen, Scale: 1, Disp: 1}, s)
		MOVQ(s, nextEmit)
		CMPQ(s, sLimitSlot)
		JGE(LabelRef("done"))
		MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)
		JMP(LabelRef("encode_loop"))
	}

	// Check long match.
	Label("check_long")
	if packed {
		checkEntry(candL, cv, tReg, "check_short")
		JMP(LabelRef("found_match"))

	} else {
		CMPL(cv.As32(), candL.As32())
		JNE(LabelRef("check_short"))
		{
			tCand := GP64()
			MOVQ(candL, tCand)
			SARQ(Imm(32), tCand)
			SUBQ(cur, tCand)
			coff := GP64()
			MOVQ(s, coff)
			SUBQ(tCand, coff)
			CMPQ(coff, maxOffSlot)
			JGE(LabelRef("check_short"))
			MOVQ(tCand, tReg)
			JMP(LabelRef("found_match"))
		}

	}
	// Check short match.
	Label("check_short")
	if packed {
		{
			tCandS := GP64()
			checkEntry(candS, cv, tCandS, "no_match")
			MOVQ(tCandS, tCandSSlot)
			MOVQ(diff, shortDiffSlot)

			// Lazy check: long match at s + 1
			cvNext := GP64()
			MOVQ(Mem{Base: src, Index: s, Scale: 1, Disp: 1}, cvNext)
			hL2 := GP64()
			hashL(cvNext, hL2)
			candL2 := GP64()
			MOVL(Mem{Base: longTab, Index: hL2, Scale: 4}, candL2.As32())
			storeEntry(s, cvNext, hL2, longTab, 1)
			tCandL2 := GP64()
			checkEntry(candL2, cvNext, tCandL2, "use_short")

			INCQ(s)
			MOVQ(tCandL2, tReg)
			JMP(LabelRef("found_match"))

			Label("use_short")
			MOVQ(shortDiffSlot, diff)
			MOVQ(tCandSSlot, tReg)
			JMP(LabelRef("found_match"))
		}

	} else {
		{
			CMPL(cv.As32(), candS.As32())
			JNE(LabelRef("no_match"))

			tCandS := GP64()
			MOVQ(candS, tCandS)
			SARQ(Imm(32), tCandS)
			SUBQ(cur, tCandS)
			coffS := GP64()
			MOVQ(s, coffS)
			SUBQ(tCandS, coffS)
			CMPQ(coffS, maxOffSlot)
			JGE(LabelRef("no_match"))
			MOVQ(tCandS, tCandSSlot)

			// Lazy check: long match at s + 1
			cvNext := GP64()
			MOVQ(Mem{Base: src, Index: s, Scale: 1, Disp: 1}, cvNext)
			hL2 := GP64()
			hashL(cvNext, hL2)
			candL2 := GP64()
			MOVQ(Mem{Base: longTab, Index: hL2, Scale: 8}, candL2)

			s1Cur := GP64()
			if fixedBias {
				LEAQ(Mem{Base: s, Disp: bias + 1}, s1Cur)
			} else {
				LEAQ(Mem{Base: s, Disp: 1}, s1Cur)
				ADDQ(cur, s1Cur)
			}
			ent2 := GP64()
			packTableEntry(s1Cur, cvNext, ent2)
			MOVQ(ent2, Mem{Base: longTab, Index: hL2, Scale: 8})

			CMPL(cvNext.As32(), candL2.As32())
			JNE(LabelRef("use_short"))
			tCandL2 := GP64()
			MOVQ(candL2, tCandL2)
			SARQ(Imm(32), tCandL2)
			SUBQ(cur, tCandL2)
			coffL2 := GP64()
			LEAQ(Mem{Base: s, Disp: 1}, coffL2)
			SUBQ(tCandL2, coffL2)
			CMPQ(coffL2, maxOffSlot)
			JGE(LabelRef("use_short"))

			INCQ(s)
			MOVQ(tCandL2, tReg)
			JMP(LabelRef("found_match"))

			Label("use_short")
			MOVQ(tCandSSlot, tReg)
			JMP(LabelRef("found_match"))
		}

	}
	// No match: advance s by 1 + ((s - nextEmit) >> 7).
	Label("no_match")
	{
		step := GP64()
		MOVQ(s, step)
		SUBQ(nextEmit, step)
		SHRQ(Imm(7), step)
		LEAQ(Mem{Base: s, Index: step, Scale: 1, Disp: 1}, s)
		CMPQ(s, sLimitSlot)
		JGE(LabelRef("done"))
		MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)
		JMP(LabelRef("encode_loop"))
	}

	// Match found at (s, tReg).
	Label("found_match")
	MOVQ(offset1, offset2Slot)
	MOVQ(s, offset1)
	SUBQ(tReg, offset1)

	var matchL reg.GPVirtual
	if packed {
		// The candidate check has already compared the first word.
		matchL = GP64()
		TESTQ(diff, diff)
		JZ(LabelRef("match_past_word"))
		BSFQ(diff, matchL)
		SHRQ(Imm(3), matchL)
		JMP(LabelRef("match_length_ready"))

		Label("match_past_word")
		aOff := GP64()
		bOff := GP64()
		LEAQ(Mem{Base: s, Disp: 8}, aOff)
		LEAQ(Mem{Base: tReg, Disp: 8}, bOff)
		h.matchLen(src, aOff, bOff, matchL, srcLenSlot)
		ADDQ(Imm(8), matchL)
		Label("match_length_ready")
	} else {
		aOff := GP64()
		bOff := GP64()
		LEAQ(Mem{Base: s, Disp: 4}, aOff)
		LEAQ(Mem{Base: tReg, Disp: 4}, bOff)
		matchL = GP64()
		h.matchLen(src, aOff, bOff, matchL, srcLenSlot)
		ADDQ(Imm(4), matchL)
	}
	hintNext(matchL, 0)

	var tMin Op = Imm(0)
	if !packed {
		minPos := GP64()
		MOVQ(s, minPos)
		SUBQ(maxOffSlot, minPos)
		JNS(LabelRef("back_tmin_ok"))
		XORQ(minPos, minPos)
		Label("back_tmin_ok")
		tMin = minPos
	}

	Label("back_loop")
	CMPQ(tReg, tMin)
	JLE(LabelRef("back_done"))
	CMPQ(s, nextEmit)
	JLE(LabelRef("back_done"))
	CMPQ(matchL, Imm(maxMatchLength))
	JAE(LabelRef("back_done"))
	b1 := GP64()
	b2 := GP64()
	MOVBQZX(Mem{Base: src, Index: tReg, Scale: 1, Disp: -1}, b1)
	MOVBQZX(Mem{Base: src, Index: s, Scale: 1, Disp: -1}, b2)
	CMPQ(b1, b2)
	JNE(LabelRef("back_done"))
	DECQ(s)
	DECQ(tReg)
	INCQ(matchL)
	JMP(LabelRef("back_loop"))

	Label("back_done")
	litLen := GP64()
	h.copyLits(src, nextEmit, s, litLen, litsPtrSlot)

	mlEmit := GP64()
	LEAQ(Mem{Base: matchL, Disp: -zstdMinMatch}, mlEmit)
	offEmit := GP64()
	LEAQ(Mem{Base: offset1, Disp: 3}, offEmit)
	{
		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		MOVL(litLen.As32(), Mem{Base: sp, Disp: 0})
		MOVL(mlEmit.As32(), Mem{Base: sp, Disp: 4})
		MOVL(offEmit.As32(), Mem{Base: sp, Disp: 8})
		ADDQ(Imm(16), sp)
		MOVQ(sp, seqPtrSlot)
	}

	ADDQ(matchL, s)
	MOVQ(s, nextEmit)
	CMPQ(s, sLimitSlot)
	JGE(LabelRef("done"))

	// Index match start+1 (long) & start+2 (short), and end-2 (long) & end-1 (short).
	{
		idx0 := GP64()
		MOVQ(s, idx0)
		SUBQ(matchL, idx0)
		INCQ(idx0)
		idx1 := GP64()
		LEAQ(Mem{Base: s, Disp: -2}, idx1)

		cv0 := GP64()
		cv1 := GP64()
		MOVQ(Mem{Base: src, Index: idx0, Scale: 1}, cv0)
		MOVQ(Mem{Base: src, Index: idx1, Scale: 1}, cv1)

		if packed {
			hh := GP64()

			hashL(cv0, hh)
			storeEntry(idx0, cv0, hh, longTab, 0)

			hashL(cv1, hh)
			storeEntry(idx1, cv1, hh, longTab, 0)

			SHRQ(Imm(8), cv0)
			SHRQ(Imm(8), cv1)
			INCQ(idx0)
			INCQ(idx1)

			hashS(cv0, hh)
			storeEntry(idx0, cv0, hh, tab, 0)

			hashS(cv1, hh)
			storeEntry(idx1, cv1, hh, tab, 0)
		} else {
			ADDQ(cur, idx0)
			ADDQ(cur, idx1)

			te := GP64()
			hh := GP64()

			hashL(cv0, hh)
			packTableEntry(idx0, cv0, te)
			MOVQ(te, Mem{Base: longTab, Index: hh, Scale: 8})

			hashL(cv1, hh)
			packTableEntry(idx1, cv1, te)
			MOVQ(te, Mem{Base: longTab, Index: hh, Scale: 8})

			SHRQ(Imm(8), cv0)
			SHRQ(Imm(8), cv1)
			INCQ(idx0)
			INCQ(idx1)

			hashS(cv0, hh)
			packTableEntry(idx0, cv0, te)
			MOVQ(te, Mem{Base: tab, Index: hh, Scale: 8})

			hashS(cv1, hh)
			packTableEntry(idx1, cv1, te)
			MOVQ(te, Mem{Base: tab, Index: hh, Scale: 8})
		}
	}

	MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)

	{
		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		CMPQ(sp, seqs4Slot)
		JBE(LabelRef("encode_loop"))
	}

	Label("rep2_loop")
	o2 := GP64()
	MOVQ(s, o2)
	SUBQ(offset2Slot, o2)
	tCheck := GP64()
	MOVL(Mem{Base: src, Index: o2, Scale: 1}, tCheck.As32())
	CMPL(tCheck.As32(), cv.As32())
	JNE(LabelRef("encode_loop"))

	{
		if packed {
			hh := GP64()
			entry := packEntry(s, cv, 0)
			hashL(cv, hh)
			MOVL(entry.As32(), Mem{Base: longTab, Index: hh, Scale: 4})
			hashS(cv, hh)
			MOVL(entry.As32(), Mem{Base: tab, Index: hh, Scale: 4})

		} else {
			sCur2 := GP64()
			if fixedBias {
				LEAQ(Mem{Base: s, Disp: bias}, sCur2)
			} else {
				MOVQ(s, sCur2)
				ADDQ(cur, sCur2)
			}
			ent := GP64()
			packTableEntry(sCur2, cv, ent)

			hh := GP64()
			hashL(cv, hh)
			MOVQ(ent, Mem{Base: longTab, Index: hh, Scale: 8})
			hashS(cv, hh)
			MOVQ(ent, Mem{Base: tab, Index: hh, Scale: 8})

		}
		aOff2 := GP64()
		bOff2 := GP64()
		LEAQ(Mem{Base: s, Disp: 4}, aOff2)
		LEAQ(Mem{Base: o2, Disp: 4}, bOff2)
		l2 := GP64()
		h.matchLen(src, aOff2, bOff2, l2, srcLenSlot)
		ADDQ(Imm(4), l2)

		ml2 := GP64()
		LEAQ(Mem{Base: l2, Disp: -zstdMinMatch}, ml2)
		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		MOVL(U32(0), Mem{Base: sp, Disp: 0})
		MOVL(ml2.As32(), Mem{Base: sp, Disp: 4})
		MOVL(U32(1), Mem{Base: sp, Disp: 8})
		ADDQ(Imm(16), sp)
		MOVQ(sp, seqPtrSlot)

		ADDQ(l2, s)
		MOVQ(s, nextEmit)

		offTmp := GP64()
		MOVQ(offset2Slot, offTmp)
		MOVQ(offset1, offset2Slot)
		MOVQ(offTmp, offset1)

		CMPQ(s, sLimitSlot)
		JGE(LabelRef("done"))
		MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)
		JMP(LabelRef("rep2_loop"))
	}

	Label("done")
	litsEnd := GP64()
	MOVQ(litsPtrSlot, litsEnd)
	SUBQ(litsBaseSlot, litsEnd)
	Store(litsEnd, ctx.Field("litsLen"))

	seqsEnd := GP64()
	MOVQ(seqPtrSlot, seqsEnd)
	SUBQ(seqsBaseSlot, seqsEnd)
	SHRQ(Imm(4), seqsEnd)
	Store(seqsEnd, ctx.Field("seqsLen"))

	Store(offset1, ctx.Field("offset1"))
	off2Out := GP64()
	MOVQ(offset2Slot, off2Out)
	Store(off2Out, ctx.Field("offset2"))
	Store(nextEmit, ctx.Field("nextEmit"))

	RET()
}

func genFastEncode(name string) {
	Package("github.com/klauspost/compress/zstd")
	TEXT(name, 0, "func(ctx *fastEncodeCtx)")
	Doc(name+" implements fastEncoder.Encode inner loop with pipelined table prefetches.", "")
	Pragma("noescape")

	ctx := Dereference(Param("ctx"))
	var h matchHelpers

	// Persistent loop registers (7 GPRs: keep table and prime6 in registers!).
	src := GP64()
	s := GP64()
	nextEmit := GP64()
	cv := GP64()
	offset1 := GP64()
	tab := GP64()
	prime6 := GP64()

	// Stack slots (promoted on arm64).
	offset2Slot := AllocLocal(8)
	srcLenSlot := AllocLocal(8)
	sLimitSlot := AllocLocal(8)
	curSlot := AllocLocal(8)
	maxOffSlot := AllocLocal(8)
	litsBaseSlot := AllocLocal(8)
	litsPtrSlot := AllocLocal(8)
	seqsBaseSlot := AllocLocal(8)
	seqPtrSlot := AllocLocal(8)
	seqs3Slot := AllocLocal(8)
	seqs4Slot := AllocLocal(8)

	Load(ctx.Field("src"), src)
	Load(ctx.Field("s"), s)
	MOVQ(s, nextEmit)
	Load(ctx.Field("offset1"), offset1)
	Load(ctx.Field("table"), tab)

	tmp := GP64()
	Load(ctx.Field("offset2"), tmp)
	MOVQ(tmp, offset2Slot)
	Load(ctx.Field("srcLen"), tmp)
	MOVQ(tmp, srcLenSlot)
	Load(ctx.Field("sLimit"), tmp)
	MOVQ(tmp, sLimitSlot)
	Load(ctx.Field("cur"), tmp)
	MOVQ(tmp, curSlot)
	Load(ctx.Field("maxMatchOff"), tmp)
	MOVQ(tmp, maxOffSlot)

	litsTmp := GP64()
	Load(ctx.Field("lits"), litsTmp)
	MOVQ(litsTmp, litsBaseSlot)
	Load(ctx.Field("litsLen"), tmp)
	ADDQ(tmp, litsTmp)
	MOVQ(litsTmp, litsPtrSlot)

	seqTmp := GP64()
	Load(ctx.Field("seqs"), seqTmp)
	MOVQ(seqTmp, seqsBaseSlot)
	LEAQ(Mem{Base: seqTmp, Disp: 32}, tmp)
	MOVQ(tmp, seqs3Slot)
	LEAQ(Mem{Base: seqTmp, Disp: 48}, tmp)
	MOVQ(tmp, seqs4Slot)
	Load(ctx.Field("seqsLen"), tmp)
	SHLQ(Imm(4), tmp)
	ADDQ(tmp, seqTmp)
	MOVQ(seqTmp, seqPtrSlot)

	MOVQ(U64(prime6bytes), prime6)

	hash6 := func(val, out reg.GPVirtual) {
		MOVQ(val, out)
		SHLQ(Imm(16), out)
		IMULQ(prime6, out)
		SHRQ(Imm(49), out)
	}

	MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)

	PCALIGN(Imm(32))
	Label("encode_loop")
	cand1 := GP64()
	cand2 := GP64()
	tReg := GP64()
	{
		h1 := GP64()
		h2 := GP64()
		hash6(cv, h1)
		{
			cv1 := GP64()
			MOVQ(cv, cv1)
			SHRQ(Imm(8), cv1)
			hash6(cv1, h2)
		}

		// Load both candidates back-to-back for 2-way MLP.
		MOVQ(Mem{Base: tab, Index: h1, Scale: 8}, cand1)
		MOVQ(Mem{Base: tab, Index: h2, Scale: 8}, cand2)

		sCur := GP64()
		MOVQ(s, sCur)
		ADDQ(curSlot, sCur)
		ent := GP64()
		packTableEntryConsumeLive(sCur, cv, ent)
		MOVQ(ent, Mem{Base: tab, Index: h1, Scale: 8})

		LEAQ(Mem{Base: s, Disp: 1}, sCur)
		ADDQ(curSlot, sCur)
		MOVQ(cv, h1) // reuse h1 for cv>>8
		SHRQ(Imm(8), h1)
		MOVQ(h1, ent)
		SHLQ(Imm(32), ent)
		SHRQ(Imm(32), ent)
		SHLQ(Imm(32), sCur)
		ORQ(sCur, ent)
		MOVQ(ent, Mem{Base: tab, Index: h2, Scale: 8})
	}

	// Check repeat offset 1 at s+2 if len(sequences) > 2.
	{
		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		CMPQ(sp, seqs3Slot)
		JBE(LabelRef("check_cand1"))
	}
	{
		repIdx := GP64()
		LEAQ(Mem{Base: s, Disp: 2}, repIdx)
		SUBQ(offset1, repIdx)
		TESTQ(repIdx, repIdx)
		JS(LabelRef("check_cand1"))

		cv16 := GP64()
		MOVQ(cv, cv16)
		SHRQ(Imm(16), cv16)
		MOVL(Mem{Base: src, Index: repIdx, Scale: 1}, tReg.As32())
		CMPL(tReg.As32(), cv16.As32())
		JNE(LabelRef("check_cand1"))

		aOff := GP64()
		bOff := GP64()
		LEAQ(Mem{Base: s, Disp: 6}, aOff)
		LEAQ(Mem{Base: repIdx, Disp: 4}, bOff)
		origLen := GP64()
		h.matchLen(src, aOff, bOff, origLen, srcLenSlot)
		ADDQ(Imm(4), origLen)

		ml := GP64()
		LEAQ(Mem{Base: origLen, Disp: -zstdMinMatch}, ml)

		start := GP64()
		LEAQ(Mem{Base: s, Disp: 2}, start)
		startLim := GP64()
		LEAQ(Mem{Base: nextEmit, Disp: 1}, startLim)
		sMin := GP64()
		MOVQ(s, sMin)
		SUBQ(maxOffSlot, sMin)
		JNS(LabelRef("rep_smin_ok"))
		XORQ(sMin, sMin)
		Label("rep_smin_ok")

		Label("rep_back_loop")
		CMPQ(repIdx, sMin)
		JLE(LabelRef("rep_back_done"))
		CMPQ(start, startLim)
		JLE(LabelRef("rep_back_done"))
		CMPQ(ml, Imm(maxMatchLength-zstdMinMatch))
		JAE(LabelRef("rep_back_done"))
		b1 := GP64()
		b2 := GP64()
		MOVBQZX(Mem{Base: src, Index: repIdx, Scale: 1, Disp: -1}, b1)
		MOVBQZX(Mem{Base: src, Index: start, Scale: 1, Disp: -1}, b2)
		CMPQ(b1, b2)
		JNE(LabelRef("rep_back_done"))
		DECQ(repIdx)
		DECQ(start)
		INCQ(ml)
		JMP(LabelRef("rep_back_loop"))

		Label("rep_back_done")
		litLen := GP64()
		h.copyLits(src, nextEmit, start, litLen, litsPtrSlot)

		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		MOVL(litLen.As32(), Mem{Base: sp, Disp: 0})
		MOVL(ml.As32(), Mem{Base: sp, Disp: 4})
		MOVL(U32(1), Mem{Base: sp, Disp: 8})
		ADDQ(Imm(16), sp)
		MOVQ(sp, seqPtrSlot)

		LEAQ(Mem{Base: s, Index: origLen, Scale: 1, Disp: 2}, s)
		MOVQ(s, nextEmit)
		CMPQ(s, sLimitSlot)
		JGE(LabelRef("done"))
		MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)
		JMP(LabelRef("encode_loop"))
	}

	// Check cand1
	Label("check_cand1")
	CMPL(cv.As32(), cand1.As32())
	JNE(LabelRef("check_cand2"))
	{
		tc := GP64()
		MOVQ(cand1, tc)
		SARQ(Imm(32), tc)
		SUBQ(curSlot, tc)
		coff := GP64()
		MOVQ(s, coff)
		SUBQ(tc, coff)
		CMPQ(coff, maxOffSlot)
		JGE(LabelRef("check_cand2"))
		MOVQ(tc, tReg)
		JMP(LabelRef("found_match"))
	}

	// Check cand2 (at s+1)
	Label("check_cand2")
	{
		cv1 := GP64()
		MOVQ(cv, cv1)
		SHRQ(Imm(8), cv1)
		CMPL(cv1.As32(), cand2.As32())
		JNE(LabelRef("no_match"))
		tc := GP64()
		MOVQ(cand2, tc)
		SARQ(Imm(32), tc)
		SUBQ(curSlot, tc)
		coff := GP64()
		LEAQ(Mem{Base: s, Disp: 1}, coff)
		SUBQ(tc, coff)
		CMPQ(coff, maxOffSlot)
		JGE(LabelRef("no_match"))
		INCQ(s)
		MOVQ(tc, tReg)
		JMP(LabelRef("found_match"))
	}

	// No match: advance s by 2 + ((s - nextEmit) >> 5).
	Label("no_match")
	{
		step := GP64()
		MOVQ(s, step)
		SUBQ(nextEmit, step)
		SHRQ(Imm(5), step)
		LEAQ(Mem{Base: s, Index: step, Scale: 1, Disp: 2}, s)
		CMPQ(s, sLimitSlot)
		JGE(LabelRef("done"))
		MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)
		JMP(LabelRef("encode_loop"))
	}

	// Found match at (s, tReg).
	Label("found_match")
	MOVQ(offset1, offset2Slot)
	MOVQ(s, offset1)
	SUBQ(tReg, offset1)

	aOff := GP64()
	bOff := GP64()
	LEAQ(Mem{Base: s, Disp: 4}, aOff)
	LEAQ(Mem{Base: tReg, Disp: 4}, bOff)
	matchL := GP64()
	h.matchLen(src, aOff, bOff, matchL, srcLenSlot)
	ADDQ(Imm(4), matchL)

	tMin := GP64()
	MOVQ(s, tMin)
	SUBQ(maxOffSlot, tMin)
	JNS(LabelRef("back_tmin_ok"))
	XORQ(tMin, tMin)
	Label("back_tmin_ok")

	Label("back_loop")
	CMPQ(tReg, tMin)
	JLE(LabelRef("back_done"))
	CMPQ(s, nextEmit)
	JLE(LabelRef("back_done"))
	CMPQ(matchL, Imm(maxMatchLength))
	JAE(LabelRef("back_done"))
	b1 := GP64()
	b2 := GP64()
	MOVBQZX(Mem{Base: src, Index: tReg, Scale: 1, Disp: -1}, b1)
	MOVBQZX(Mem{Base: src, Index: s, Scale: 1, Disp: -1}, b2)
	CMPQ(b1, b2)
	JNE(LabelRef("back_done"))
	DECQ(s)
	DECQ(tReg)
	INCQ(matchL)
	JMP(LabelRef("back_loop"))

	Label("back_done")
	litLen := GP64()
	h.copyLits(src, nextEmit, s, litLen, litsPtrSlot)

	mlEmit := GP64()
	LEAQ(Mem{Base: matchL, Disp: -zstdMinMatch}, mlEmit)
	offEmit := GP64()
	LEAQ(Mem{Base: offset1, Disp: 3}, offEmit)
	{
		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		MOVL(litLen.As32(), Mem{Base: sp, Disp: 0})
		MOVL(mlEmit.As32(), Mem{Base: sp, Disp: 4})
		MOVL(offEmit.As32(), Mem{Base: sp, Disp: 8})
		ADDQ(Imm(16), sp)
		MOVQ(sp, seqPtrSlot)
	}

	ADDQ(matchL, s)
	MOVQ(s, nextEmit)
	CMPQ(s, sLimitSlot)
	JGE(LabelRef("done"))
	MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)

	{
		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		CMPQ(sp, seqs4Slot)
		JBE(LabelRef("encode_loop"))
	}

	o2 := GP64()
	MOVQ(s, o2)
	SUBQ(offset2Slot, o2)
	tCheck := GP64()
	MOVL(Mem{Base: src, Index: o2, Scale: 1}, tCheck.As32())
	CMPL(tCheck.As32(), cv.As32())
	JNE(LabelRef("encode_loop"))

	{
		aOff2 := GP64()
		bOff2 := GP64()
		LEAQ(Mem{Base: s, Disp: 4}, aOff2)
		LEAQ(Mem{Base: o2, Disp: 4}, bOff2)
		l2 := GP64()
		h.matchLen(src, aOff2, bOff2, l2, srcLenSlot)
		ADDQ(Imm(4), l2)

		hh := GP64()
		hash6(cv, hh)
		sCur2 := GP64()
		MOVQ(s, sCur2)
		ADDQ(curSlot, sCur2)
		ent2 := GP64()
		packTableEntry(sCur2, cv, ent2)
		MOVQ(ent2, Mem{Base: tab, Index: hh, Scale: 8})

		ml2 := GP64()
		LEAQ(Mem{Base: l2, Disp: -zstdMinMatch}, ml2)
		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		MOVL(U32(0), Mem{Base: sp, Disp: 0})
		MOVL(ml2.As32(), Mem{Base: sp, Disp: 4})
		MOVL(U32(1), Mem{Base: sp, Disp: 8})
		ADDQ(Imm(16), sp)
		MOVQ(sp, seqPtrSlot)

		ADDQ(l2, s)
		MOVQ(s, nextEmit)
		offTmp := GP64()
		MOVQ(offset2Slot, offTmp)
		MOVQ(offset1, offset2Slot)
		MOVQ(offTmp, offset1)
		CMPQ(s, sLimitSlot)
		JGE(LabelRef("done"))
		MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)
		JMP(LabelRef("encode_loop"))
	}

	Label("done")
	litsEnd := GP64()
	MOVQ(litsPtrSlot, litsEnd)
	SUBQ(litsBaseSlot, litsEnd)
	Store(litsEnd, ctx.Field("litsLen"))

	seqsEnd := GP64()
	MOVQ(seqPtrSlot, seqsEnd)
	SUBQ(seqsBaseSlot, seqsEnd)
	SHRQ(Imm(4), seqsEnd)
	Store(seqsEnd, ctx.Field("seqsLen"))

	Store(offset1, ctx.Field("offset1"))
	off2Out := GP64()
	MOVQ(offset2Slot, off2Out)
	Store(off2Out, ctx.Field("offset2"))
	Store(nextEmit, ctx.Field("nextEmit"))

	RET()
}

func packAbsoluteEntry(offPlusCur, val, out reg.GPVirtual) {
	t := GP64()
	// A 32-bit LEA clears the upper bits even when out aliases val.
	LEAL(Mem{Base: val}, out.As32())
	MOVQ(offPlusCur, t)
	SHLQ(Imm(32), t)
	ORQ(t, out)
}

// packAbsoluteEntryConsume packs the entry and clobbers offPlusCur.
func packAbsoluteEntryConsume(offPlusCur, val, out reg.GPVirtual) {
	LEAL(Mem{Base: val}, out.As32())
	SHLQ(Imm(32), offPlusCur)
	ORQ(offPlusCur, out)
}

func packAbsolutePrevEntry(offPlusCur, prevCand, out reg.GPVirtual) {
	// prevEntry{offset: offPlusCur (low 32), prev: prevCand.offset (low 32 of prevCand -> high 32)}
	t := GP64()
	LEAL(Mem{Base: offPlusCur}, out.As32())
	MOVQ(prevCand, t)
	SHLQ(Imm(32), t)
	ORQ(t, out)
}

func genBetterFastEncode(name string) {
	Package("github.com/klauspost/compress/zstd")
	TEXT(name, 0, "func(ctx *betterEncodeCtx)")
	Doc(name+" implements betterFastEncoder.Encode inner loop with pipelined table prefetches.", "")
	Pragma("noescape")

	ctx := Dereference(Param("ctx"))
	var h matchHelpers

	// Persistent loop registers (7 GPRs: keep both table pointers in registers!).
	src := GP64()
	s := GP64()
	nextEmit := GP64()
	cv := GP64()
	offset1 := GP64()
	tab := GP64()
	longTab := GP64()

	// Stack slots (promoted on arm64).
	offset2Slot := AllocLocal(8)
	candLSlot := AllocLocal(8)
	candSSlot := AllocLocal(8)
	index0Slot := AllocLocal(8)
	matchedSlot := AllocLocal(8)
	tSlot := AllocLocal(8)
	srcLenSlot := AllocLocal(8)
	sLimitSlot := AllocLocal(8)
	curSlot := AllocLocal(8)
	maxOffSlot := AllocLocal(8)
	litsBaseSlot := AllocLocal(8)
	litsPtrSlot := AllocLocal(8)
	seqsBaseSlot := AllocLocal(8)
	seqPtrSlot := AllocLocal(8)
	seqs3Slot := AllocLocal(8)
	seqs4Slot := AllocLocal(8)
	primeLSlot := AllocLocal(8)
	primeSSlot := AllocLocal(8)
	nextSSlot := AllocLocal(8)
	nextCVSlot := AllocLocal(8)

	// Search using the same absolute offsets stored in the hash tables.
	// Subtracting the cursor from the base keeps base+offset at the
	// original input byte, and differences between offsets are unchanged.
	bias := GP64()
	Load(ctx.Field("cur"), bias)
	Load(ctx.Field("src"), src)
	SUBQ(bias, src)
	Load(ctx.Field("s"), s)
	ADDQ(bias, s)
	MOVQ(s, nextEmit)
	Load(ctx.Field("offset1"), offset1)
	Load(ctx.Field("table"), tab)
	Load(ctx.Field("longTable"), longTab)

	tmp := GP64()
	Load(ctx.Field("offset2"), tmp)
	MOVQ(tmp, offset2Slot)
	Load(ctx.Field("srcLen"), tmp)
	ADDQ(bias, tmp)
	MOVQ(tmp, srcLenSlot)
	Load(ctx.Field("sLimit"), tmp)
	ADDQ(bias, tmp)
	MOVQ(tmp, sLimitSlot)
	MOVQ(bias, curSlot)
	Load(ctx.Field("maxMatchOff"), tmp)
	MOVQ(tmp, maxOffSlot)

	litsTmp := GP64()
	Load(ctx.Field("lits"), litsTmp)
	MOVQ(litsTmp, litsBaseSlot)
	Load(ctx.Field("litsLen"), tmp)
	ADDQ(tmp, litsTmp)
	MOVQ(litsTmp, litsPtrSlot)

	seqTmp := GP64()
	Load(ctx.Field("seqs"), seqTmp)
	MOVQ(seqTmp, seqsBaseSlot)
	LEAQ(Mem{Base: seqTmp, Disp: 32}, tmp)
	MOVQ(tmp, seqs3Slot)
	LEAQ(Mem{Base: seqTmp, Disp: 48}, tmp)
	MOVQ(tmp, seqs4Slot)
	Load(ctx.Field("seqsLen"), tmp)
	SHLQ(Imm(4), tmp)
	ADDQ(tmp, seqTmp)
	MOVQ(seqTmp, seqPtrSlot)

	MOVQ(U64(prime8bytes), tmp)
	MOVQ(tmp, primeLSlot)
	MOVQ(U64(prime5bytes<<24), tmp)
	MOVQ(tmp, primeSSlot)

	// betterHashL: 19 bits -> >> 45; betterHashS: 13 bits -> >> 51
	hashL := func(val, out reg.GPVirtual) {
		MOVQ(val, out)
		IMULQ(primeLSlot, out)
		SHRQ(Imm(45), out) // 64 - 19
	}
	hashS := func(val, out reg.GPVirtual) {
		MOVQ(val, out)
		IMULQ(primeSSlot, out)
		SHRQ(Imm(51), out) // 64 - 13
	}

	// Index skipped loop: for end := s - 1; idx < end; idx += 2
	emitIndexSkipped := func(idxStart reg.GPVirtual) {
		end := GP64()
		LEAQ(Mem{Base: s, Disp: -1}, end)
		lLoop := h.lbl("idx_loop")
		lDone := h.lbl("idx_done")
		CMPQ(idxStart, end)
		JGE(LabelRef(lDone))

		cv0 := GP64()
		hL := GP64()
		MOVQ(Mem{Base: src, Index: idxStart, Scale: 1}, cv0)
		hashL(cv0, hL)

		Label(lLoop)
		// Fetch the previous position now. The next hash is independent
		// of it, and the bucket load for that hash stays in the next
		// iteration so adjacent collisions retain the ordered updates.
		oldL := GP64()
		MOVL(Mem{Base: longTab, Index: hL, Scale: 8}, oldL.As32())
		next := GP64()
		nextHash := GP64()
		// idxStart <= s-2, so this read is safe even on the last pair.
		MOVQ(Mem{Base: src, Index: idxStart, Scale: 1, Disp: 2}, next)
		hashL(next, nextHash)
		PREFETCHT0(Mem{Base: longTab, Index: nextHash, Scale: 8})

		SHLQ(Imm(32), oldL)
		off := GP64()
		LEAL(Mem{Base: idxStart}, off.As32())
		ORQ(off, oldL)
		MOVQ(oldL, Mem{Base: longTab, Index: hL, Scale: 8})
		MOVQ(nextHash, hL)

		SHRQ(Imm(8), cv0)
		hS := GP64()
		hashS(cv0, hS)
		off1 := GP64()
		LEAQ(Mem{Base: idxStart, Disp: 1}, off1)
		newS := GP64()
		packAbsoluteEntryConsume(off1, cv0, newS)
		MOVQ(newS, Mem{Base: tab, Index: hS, Scale: 8})

		MOVQ(next, cv0)
		ADDQ(Imm(2), idxStart)
		CMPQ(idxStart, end)
		JL(LabelRef(lLoop))

		Label(lDone)
	}

	// Normal match extension leaves the next search position unchanged.
	hintNext := func(length reg.GPVirtual, displacement int) {
		done := h.lbl("hint_next_done")
		next := GP64()
		LEAQ(Mem{Base: s, Index: length, Scale: 1, Disp: displacement}, next)
		CMPQ(next, sLimitSlot)
		JGE(LabelRef(done))
		word := GP64()
		MOVQ(Mem{Base: src, Index: next, Scale: 1}, word)
		hashL(word, word)
		PREFETCHT0(Mem{Base: longTab, Index: word, Scale: 8})
		Label(done)
	}

	MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)

	PCALIGN(Imm(32))
	Label("encode_loop")
	{
		hL := GP64()
		hS := GP64()
		hashL(cv, hL)
		hashS(cv, hS)

		candL := GP64()
		candS := GP64()
		MOVQ(Mem{Base: longTab, Index: hL, Scale: 8}, candL)
		MOVQ(Mem{Base: tab, Index: hS, Scale: 8}, candS)
		MOVQ(candL, candLSlot)
		MOVQ(candS, candSSlot)

		sCur := GP64()
		MOVQ(s, sCur)

		newL := GP64()
		packAbsolutePrevEntry(sCur, candL, newL)
		MOVQ(newL, Mem{Base: longTab, Index: hL, Scale: 8})

		newS := GP64()
		packAbsoluteEntry(sCur, cv, newS)
		MOVQ(newS, Mem{Base: tab, Index: hS, Scale: 8})

		// index0 = s + 1
		idx0 := GP64()
		LEAQ(Mem{Base: s, Disp: 1}, idx0)
		MOVQ(idx0, index0Slot)
	}

	// Check repeat offset 1 at s+1 if len(sequences) > 2.
	{
		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		CMPQ(sp, seqs3Slot)
		JBE(LabelRef("check_long"))
	}
	{
		repIdx := GP64()
		MOVQ(s, repIdx)
		SUBQ(offset1, repIdx)
		INCQ(repIdx)
		CMPQ(repIdx, curSlot)
		JL(LabelRef("check_long"))

		cvShifted := GP64()
		MOVQ(cv, cvShifted)
		SHRQ(Imm(8), cvShifted)
		t32 := GP64()
		MOVL(Mem{Base: src, Index: repIdx, Scale: 1}, t32.As32())
		CMPL(t32.As32(), cvShifted.As32())
		JNE(LabelRef("check_long"))

		aOff := GP64()
		bOff := GP64()
		LEAQ(Mem{Base: s, Disp: 5}, aOff)
		LEAQ(Mem{Base: repIdx, Disp: 4}, bOff)
		origLen := GP64()
		h.matchLen(src, aOff, bOff, origLen, srcLenSlot)
		ADDQ(Imm(4), origLen)
		hintNext(origLen, 1)

		ml := GP64()
		LEAQ(Mem{Base: origLen, Disp: -zstdMinMatch}, ml)

		start := GP64()
		LEAQ(Mem{Base: s, Disp: 1}, start)
		startLim := GP64()
		LEAQ(Mem{Base: nextEmit, Disp: 1}, startLim)
		tMin := GP64()
		MOVQ(s, tMin)
		SUBQ(maxOffSlot, tMin)
		origin := GP64()
		MOVQ(curSlot, origin)
		CMPQ(tMin, origin)
		CMOVQLT(origin, tMin)

		Label("rep_back_loop")
		CMPQ(repIdx, tMin)
		JLE(LabelRef("rep_back_done"))
		CMPQ(start, startLim)
		JLE(LabelRef("rep_back_done"))
		CMPQ(ml, Imm(maxMatchLength-zstdMinMatch-1))
		JAE(LabelRef("rep_back_done"))
		b1 := GP64()
		b2 := GP64()
		MOVBQZX(Mem{Base: src, Index: repIdx, Scale: 1, Disp: -1}, b1)
		MOVBQZX(Mem{Base: src, Index: start, Scale: 1, Disp: -1}, b2)
		CMPQ(b1, b2)
		JNE(LabelRef("rep_back_done"))
		DECQ(repIdx)
		DECQ(start)
		INCQ(ml)
		JMP(LabelRef("rep_back_loop"))

		Label("rep_back_done")
		litLen := GP64()
		h.copyLits(src, nextEmit, start, litLen, litsPtrSlot)

		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		MOVL(litLen.As32(), Mem{Base: sp, Disp: 0})
		MOVL(ml.As32(), Mem{Base: sp, Disp: 4})
		MOVL(U32(1), Mem{Base: sp, Disp: 8})
		ADDQ(Imm(16), sp)
		MOVQ(sp, seqPtrSlot)

		idx0 := GP64()
		LEAQ(Mem{Base: s, Disp: 1}, idx0)
		LEAQ(Mem{Base: s, Index: origLen, Scale: 1, Disp: 1}, s)
		MOVQ(s, nextEmit)
		CMPQ(s, sLimitSlot)
		JGE(LabelRef("done"))
		emitIndexSkipped(idx0)
		MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)
		JMP(LabelRef("encode_loop"))
	}

	// Check long match (offset & prev).
	Label("check_long")
	{
		candL := GP64()
		MOVQ(candLSlot, candL)
		// The entry already holds an absolute offset.
		coffL := GP64()
		MOVLQSX(candL.As32(), coffL)
		CMPQ(coffL, curSlot)
		JL(LabelRef("check_long_prev"))
		delta := GP64()
		MOVQ(s, delta)
		SUBQ(coffL, delta)
		CMPQ(delta, maxOffSlot)
		JGE(LabelRef("check_long_prev"))
		CMPQ(cv, Mem{Base: src, Index: coffL, Scale: 1})
		JNE(LabelRef("check_long_prev"))

		// Long match found on offset!
		aOff := GP64()
		bOff := GP64()
		LEAQ(Mem{Base: s, Disp: 8}, aOff)
		LEAQ(Mem{Base: coffL, Disp: 8}, bOff)
		ml := GP64()
		h.matchLen(src, aOff, bOff, ml, srcLenSlot)
		ADDQ(Imm(8), ml)
		MOVQ(ml, matchedSlot)
		MOVQ(coffL, tSlot)

		// Also check prev to see if it's longer!
		candL2 := GP64()
		MOVQ(candLSlot, candL2)
		SARQ(Imm(32), candL2)
		CMPQ(candL2, curSlot)
		JL(LabelRef("found_match"))
		d2 := GP64()
		MOVQ(s, d2)
		SUBQ(candL2, d2)
		CMPQ(d2, maxOffSlot)
		JGE(LabelRef("found_match"))
		CMPQ(cv, Mem{Base: src, Index: candL2, Scale: 1})
		JNE(LabelRef("found_match"))

		LEAQ(Mem{Base: s, Disp: 8}, aOff)
		LEAQ(Mem{Base: candL2, Disp: 8}, bOff)
		ml2 := GP64()
		h.matchLen(src, aOff, bOff, ml2, srcLenSlot)
		ADDQ(Imm(8), ml2)
		CMPQ(ml2, matchedSlot)
		JLE(LabelRef("found_match"))
		MOVQ(ml2, matchedSlot)
		MOVQ(candL2, tSlot)
		JMP(LabelRef("found_match"))
	}

	Label("check_long_prev")
	{
		candLP := GP64()
		MOVQ(candLSlot, candLP)
		SARQ(Imm(32), candLP)
		CMPQ(candLP, curSlot)
		JL(LabelRef("check_short"))
		delta := GP64()
		MOVQ(s, delta)
		SUBQ(candLP, delta)
		CMPQ(delta, maxOffSlot)
		JGE(LabelRef("check_short"))
		CMPQ(cv, Mem{Base: src, Index: candLP, Scale: 1})
		JNE(LabelRef("check_short"))

		aOff := GP64()
		bOff := GP64()
		LEAQ(Mem{Base: s, Disp: 8}, aOff)
		LEAQ(Mem{Base: candLP, Disp: 8}, bOff)
		ml := GP64()
		h.matchLen(src, aOff, bOff, ml, srcLenSlot)
		ADDQ(Imm(8), ml)
		MOVQ(ml, matchedSlot)
		MOVQ(candLP, tSlot)
		JMP(LabelRef("found_match"))
	}

	// Check short match.
	Label("check_short")
	{
		candS := GP64()
		MOVQ(candSSlot, candS)
		CMPL(cv.As32(), candS.As32())
		JNE(LabelRef("no_match"))
		coffS := GP64()
		MOVQ(candS, coffS)
		SARQ(Imm(32), coffS)
		CMPQ(coffS, curSlot)
		JL(LabelRef("no_match"))
		delta := GP64()
		MOVQ(s, delta)
		SUBQ(coffS, delta)
		CMPQ(delta, maxOffSlot)
		JGE(LabelRef("no_match"))

		// Short match found: compute its length and save (coffS, matched).
		aOff := GP64()
		bOff := GP64()
		LEAQ(Mem{Base: s, Disp: 4}, aOff)
		LEAQ(Mem{Base: coffS, Disp: 4}, bOff)
		ml := GP64()
		h.matchLen(src, aOff, bOff, ml, srcLenSlot)
		ADDQ(Imm(4), ml)
		MOVQ(ml, matchedSlot)
		MOVQ(coffS, tSlot)

		// Lazy check at s+1 in longTable (both offset and prev).
		cv1 := GP64()
		MOVQ(Mem{Base: src, Index: s, Scale: 1, Disp: 1}, cv1)
		hL1 := GP64()
		hashL(cv1, hL1)
		cL1 := GP64()
		MOVQ(Mem{Base: longTab, Index: hL1, Scale: 8}, cL1)
		MOVQ(cL1, candLSlot)

		s1Cur := GP64()
		LEAQ(Mem{Base: s, Disp: 1}, s1Cur)
		newL1 := GP64()
		packAbsolutePrevEntry(s1Cur, cL1, newL1)
		MOVQ(newL1, Mem{Base: longTab, Index: hL1, Scale: 8})

		// Check cL1.offset (note Go checks s-coffsetL < maxMatchOff)
		coffL1 := GP64()
		MOVLQSX(cL1.As32(), coffL1)
		CMPQ(coffL1, curSlot)
		JL(LabelRef("lazy_check_prev"))
		d1 := GP64()
		MOVQ(s, d1)
		SUBQ(coffL1, d1)
		CMPQ(d1, maxOffSlot)
		JGE(LabelRef("lazy_check_prev"))
		CMPQ(cv1, Mem{Base: src, Index: coffL1, Scale: 1})
		JNE(LabelRef("lazy_check_prev"))

		LEAQ(Mem{Base: s, Disp: 9}, aOff)
		LEAQ(Mem{Base: coffL1, Disp: 8}, bOff)
		ml1 := GP64()
		h.matchLen(src, aOff, bOff, ml1, srcLenSlot)
		ADDQ(Imm(8), ml1)
		CMPQ(ml1, matchedSlot)
		JLE(LabelRef("lazy_check_prev"))
		INCQ(s)
		MOVQ(ml1, matchedSlot)
		MOVQ(coffL1, tSlot)
		JMP(LabelRef("found_match"))

		Label("lazy_check_prev")
		cLP1 := GP64()
		MOVQ(candLSlot, cLP1)
		SARQ(Imm(32), cLP1)
		CMPQ(cLP1, curSlot)
		JL(LabelRef("found_match"))
		d2 := GP64()
		MOVQ(s, d2)
		SUBQ(cLP1, d2)
		CMPQ(d2, maxOffSlot)
		JGE(LabelRef("found_match"))
		cv1b := GP64()
		MOVQ(Mem{Base: src, Index: s, Scale: 1, Disp: 1}, cv1b)
		CMPQ(cv1b, Mem{Base: src, Index: cLP1, Scale: 1})
		JNE(LabelRef("found_match"))

		LEAQ(Mem{Base: s, Disp: 9}, aOff)
		LEAQ(Mem{Base: cLP1, Disp: 8}, bOff)
		ml2 := GP64()
		h.matchLen(src, aOff, bOff, ml2, srcLenSlot)
		ADDQ(Imm(8), ml2)
		CMPQ(ml2, matchedSlot)
		JLE(LabelRef("found_match"))
		INCQ(s)
		MOVQ(ml2, matchedSlot)
		MOVQ(cLP1, tSlot)
		JMP(LabelRef("found_match"))
	}

	// No match: advance s by 1 + ((s - nextEmit) >> 8).
	Label("no_match")
	{
		step := GP64()
		MOVQ(s, step)
		SUBQ(nextEmit, step)
		SHRQ(Imm(8), step)
		LEAQ(Mem{Base: s, Index: step, Scale: 1, Disp: 1}, s)
		CMPQ(s, sLimitSlot)
		JGE(LabelRef("done"))
		MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)
		JMP(LabelRef("encode_loop"))
	}

	// Match found! Try end-of-match long search (skipBeginning = 3).
	Label("found_match")
	{
		sEnd := GP64()
		MOVQ(s, sEnd)
		ADDQ(matchedSlot, sEnd)
		CMPQ(sEnd, sLimitSlot)
		JGE(LabelRef("end_search_done"))

		cvEnd := GP64()
		MOVQ(Mem{Base: src, Index: sEnd, Scale: 1}, cvEnd)
		hLEnd := GP64()
		hashL(cvEnd, hLEnd)
		cEnd := GP64()
		MOVQ(Mem{Base: longTab, Index: hLEnd, Scale: 8}, cEnd)
		MOVQ(cEnd, candLSlot)

		// s2 = s + 3; cv2 = load32(src, s2)
		s2 := GP64()
		LEAQ(Mem{Base: s, Disp: 3}, s2)
		MOVQ(s2, nextSSlot)
		cv2 := GP64()
		MOVL(Mem{Base: src, Index: s2, Scale: 1}, cv2.As32())
		MOVQ(cv2, nextCVSlot)

		// coff = cEnd.offset - matched + 3
		coff := GP64()
		MOVLQSX(cEnd.As32(), coff)
		SUBQ(matchedSlot, coff)
		ADDQ(Imm(3), coff)
		CMPQ(coff, curSlot)
		JL(LabelRef("end_search_prev"))
		CMPQ(coff, s2)
		JGE(LabelRef("end_search_prev"))
		d := GP64()
		MOVQ(s2, d)
		SUBQ(coff, d)
		CMPQ(d, maxOffSlot)
		JGE(LabelRef("end_search_prev"))
		t32 := GP64()
		MOVL(Mem{Base: src, Index: coff, Scale: 1}, t32.As32())
		CMPL(t32.As32(), cv2.As32())
		JNE(LabelRef("end_search_prev"))

		aOff := GP64()
		bOff := GP64()
		LEAQ(Mem{Base: s2, Disp: 4}, aOff)
		LEAQ(Mem{Base: coff, Disp: 4}, bOff)
		mlEnd := GP64()
		h.matchLen(src, aOff, bOff, mlEnd, srcLenSlot)
		ADDQ(Imm(4), mlEnd)
		CMPQ(mlEnd, matchedSlot)
		JLE(LabelRef("end_search_prev"))
		MOVQ(coff, tSlot)
		MOVQ(s2, s)
		MOVQ(mlEnd, matchedSlot)

		Label("end_search_prev")
		// Check candidateL.prev using saved s2 and cv2 (matching enc_better.go:459-472).
		s2p := GP64()
		MOVQ(nextSSlot, s2p)
		coffP := GP64()
		MOVQ(candLSlot, coffP)
		SARQ(Imm(32), coffP)
		SUBQ(matchedSlot, coffP)
		ADDQ(Imm(3), coffP)
		CMPQ(coffP, curSlot)
		JL(LabelRef("end_search_done"))
		CMPQ(coffP, s2p)
		JGE(LabelRef("end_search_done"))
		dp := GP64()
		MOVQ(s2p, dp)
		SUBQ(coffP, dp)
		CMPQ(dp, maxOffSlot)
		JGE(LabelRef("end_search_done"))
		cv2p := GP64()
		MOVQ(nextCVSlot, cv2p)
		t32p := GP64()
		MOVL(Mem{Base: src, Index: coffP, Scale: 1}, t32p.As32())
		CMPL(t32p.As32(), cv2p.As32())
		JNE(LabelRef("end_search_done"))

		aOffP := GP64()
		bOffP := GP64()
		LEAQ(Mem{Base: s2p, Disp: 4}, aOffP)
		LEAQ(Mem{Base: coffP, Disp: 4}, bOffP)
		mlEndP := GP64()
		h.matchLen(src, aOffP, bOffP, mlEndP, srcLenSlot)
		ADDQ(Imm(4), mlEndP)
		CMPQ(mlEndP, matchedSlot)
		JLE(LabelRef("end_search_done"))
		MOVQ(coffP, tSlot)
		MOVQ(s2p, s)
		MOVQ(mlEndP, matchedSlot)
	}
	Label("end_search_done")

	// Update offsets and extend backwards.
	tReg := GP64()
	MOVQ(tSlot, tReg)
	matchL := GP64()
	MOVQ(matchedSlot, matchL)
	hintNext(matchL, 0)

	MOVQ(offset1, offset2Slot)
	MOVQ(s, offset1)
	SUBQ(tReg, offset1)

	tMin := GP64()
	MOVQ(s, tMin)
	SUBQ(maxOffSlot, tMin)
	origin := GP64()
	MOVQ(curSlot, origin)
	CMPQ(tMin, origin)
	CMOVQLT(origin, tMin)

	Label("back_loop")
	CMPQ(tReg, tMin)
	JLE(LabelRef("back_done"))
	CMPQ(s, nextEmit)
	JLE(LabelRef("back_done"))
	CMPQ(matchL, Imm(maxMatchLength))
	JAE(LabelRef("back_done"))
	b1 := GP64()
	b2 := GP64()
	MOVBQZX(Mem{Base: src, Index: tReg, Scale: 1, Disp: -1}, b1)
	MOVBQZX(Mem{Base: src, Index: s, Scale: 1, Disp: -1}, b2)
	CMPQ(b1, b2)
	JNE(LabelRef("back_done"))
	DECQ(s)
	DECQ(tReg)
	INCQ(matchL)
	JMP(LabelRef("back_loop"))

	Label("back_done")
	litLen := GP64()
	h.copyLits(src, nextEmit, s, litLen, litsPtrSlot)

	mlEmit := GP64()
	LEAQ(Mem{Base: matchL, Disp: -zstdMinMatch}, mlEmit)
	offEmit := GP64()
	LEAQ(Mem{Base: offset1, Disp: 3}, offEmit)
	{
		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		MOVL(litLen.As32(), Mem{Base: sp, Disp: 0})
		MOVL(mlEmit.As32(), Mem{Base: sp, Disp: 4})
		MOVL(offEmit.As32(), Mem{Base: sp, Disp: 8})
		ADDQ(Imm(16), sp)
		MOVQ(sp, seqPtrSlot)
	}

	ADDQ(matchL, s)
	MOVQ(s, nextEmit)
	CMPQ(s, sLimitSlot)
	JGE(LabelRef("done"))

	// Index match start+1 .. s-1 (step 2).
	{
		idx0 := GP64()
		MOVQ(index0Slot, idx0)
		emitIndexSkipped(idx0)
	}

	MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)

	{
		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		CMPQ(sp, seqs4Slot)
		JBE(LabelRef("encode_loop"))
	}

	Label("rep2_loop")
	o2 := GP64()
	MOVQ(s, o2)
	SUBQ(offset2Slot, o2)
	tCheck := GP64()
	MOVL(Mem{Base: src, Index: o2, Scale: 1}, tCheck.As32())
	CMPL(tCheck.As32(), cv.As32())
	JNE(LabelRef("encode_loop"))

	{
		aOff2 := GP64()
		bOff2 := GP64()
		LEAQ(Mem{Base: s, Disp: 4}, aOff2)
		LEAQ(Mem{Base: o2, Disp: 4}, bOff2)
		l2 := GP64()
		h.matchLen(src, aOff2, bOff2, l2, srcLenSlot)
		ADDQ(Imm(4), l2)

		sCur2 := GP64()
		MOVQ(s, sCur2)
		hh := GP64()
		hashL(cv, hh)
		oldL := GP64()
		MOVQ(Mem{Base: longTab, Index: hh, Scale: 8}, oldL)
		newL := GP64()
		packAbsolutePrevEntry(sCur2, oldL, newL)
		MOVQ(newL, Mem{Base: longTab, Index: hh, Scale: 8})

		hashS(cv, hh)
		newS := GP64()
		packAbsoluteEntry(sCur2, cv, newS)
		MOVQ(newS, Mem{Base: tab, Index: hh, Scale: 8})

		ml2 := GP64()
		LEAQ(Mem{Base: l2, Disp: -zstdMinMatch}, ml2)
		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		MOVL(U32(0), Mem{Base: sp, Disp: 0})
		MOVL(ml2.As32(), Mem{Base: sp, Disp: 4})
		MOVL(U32(1), Mem{Base: sp, Disp: 8})
		ADDQ(Imm(16), sp)
		MOVQ(sp, seqPtrSlot)

		ADDQ(l2, s)
		MOVQ(s, nextEmit)
		offTmp := GP64()
		MOVQ(offset2Slot, offTmp)
		MOVQ(offset1, offset2Slot)
		MOVQ(offTmp, offset1)

		CMPQ(s, sLimitSlot)
		JGE(LabelRef("done"))
		MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)
		JMP(LabelRef("rep2_loop"))
	}

	Label("done")
	litsEnd := GP64()
	MOVQ(litsPtrSlot, litsEnd)
	SUBQ(litsBaseSlot, litsEnd)
	Store(litsEnd, ctx.Field("litsLen"))

	seqsEnd := GP64()
	MOVQ(seqPtrSlot, seqsEnd)
	SUBQ(seqsBaseSlot, seqsEnd)
	SHRQ(Imm(4), seqsEnd)
	Store(seqsEnd, ctx.Field("seqsLen"))

	Store(offset1, ctx.Field("offset1"))
	off2Out := GP64()
	MOVQ(offset2Slot, off2Out)
	Store(off2Out, ctx.Field("offset2"))
	SUBQ(curSlot, nextEmit)
	Store(nextEmit, ctx.Field("nextEmit"))

	RET()
}

func genBestFastEncode(name string) {
	Package("github.com/klauspost/compress/zstd")
	TEXT(name, 0, "func(ctx *bestEncodeCtx)")
	Doc(name+" implements bestFastEncoder.Encode inner loop with 32MB longTable prefetching.", "")
	Pragma("noescape")

	ctx := Dereference(Param("ctx"))
	var h matchHelpers

	// Persistent loop registers (6 GPRs).
	src := GP64()
	s := GP64()
	nextEmit := GP64()
	cv := GP64()
	tab := GP64()
	longTab := GP64()

	// Stack slots (promoted to upper regs on arm64).
	srcLenSlot := AllocLocal(8)
	sLimitSlot := AllocLocal(8)
	curSlot := AllocLocal(8)
	maxOffSlot := AllocLocal(8)
	bpbSlot := AllocLocal(8)
	litsBaseSlot := AllocLocal(8)
	litsPtrSlot := AllocLocal(8)
	seqsBaseSlot := AllocLocal(8)
	seqPtrSlot := AllocLocal(8)
	seqs3Slot := AllocLocal(8)
	offset1Slot := AllocLocal(8)
	offset2Slot := AllocLocal(8)
	offset3Slot := AllocLocal(8)
	predefOFSlot := AllocLocal(8)
	predefMLDirectSlot := AllocLocal(8)
	predefMLCostSlot := AllocLocal(8)
	primeLSlot := AllocLocal(8)
	primeSSlot := AllocLocal(8)
	hLSlot := AllocLocal(8)
	hSSlot := AllocLocal(8)
	candLSlot := AllocLocal(8)
	candSSlot := AllocLocal(8)
	candEndSlot := AllocLocal(8)
	index0Slot := AllocLocal(8)
	bestOffSlot := AllocLocal(8)
	bestSSlot := AllocLocal(8)
	bestLenSlot := AllocLocal(8)
	bestRepSlot := AllocLocal(8)
	bestEstSlot := AllocLocal(8)
	qCurSlot := AllocLocal(8)
	qEndSlot := AllocLocal(8)
	stageSlot := AllocLocal(8)
	candBuf := AllocLocal(6 * 16) // up to 6 entries of {off int32, s int32, rep int32, pad int32}

	Load(ctx.Field("src"), src)
	Load(ctx.Field("s"), s)
	MOVQ(s, nextEmit)
	Load(ctx.Field("table"), tab)
	Load(ctx.Field("longTable"), longTab)

	tmp := GP64()
	Load(ctx.Field("srcLen"), tmp)
	MOVQ(tmp, srcLenSlot)
	Load(ctx.Field("sLimit"), tmp)
	MOVQ(tmp, sLimitSlot)
	Load(ctx.Field("cur"), tmp)
	MOVQ(tmp, curSlot)
	Load(ctx.Field("maxMatchOff"), tmp)
	MOVQ(tmp, maxOffSlot)
	Load(ctx.Field("bitsPerByte"), tmp)
	MOVQ(tmp, bpbSlot)
	Load(ctx.Field("offset1"), tmp)
	MOVQ(tmp, offset1Slot)
	Load(ctx.Field("offset2"), tmp)
	MOVQ(tmp, offset2Slot)
	Load(ctx.Field("offset3"), tmp)
	MOVQ(tmp, offset3Slot)
	Load(ctx.Field("predefOF"), tmp)
	MOVQ(tmp, predefOFSlot)
	Load(ctx.Field("predefMLDirect"), tmp)
	MOVQ(tmp, predefMLDirectSlot)
	Load(ctx.Field("predefMLCost"), tmp)
	MOVQ(tmp, predefMLCostSlot)

	litsTmp := GP64()
	Load(ctx.Field("lits"), litsTmp)
	MOVQ(litsTmp, litsBaseSlot)
	Load(ctx.Field("litsLen"), tmp)
	ADDQ(tmp, litsTmp)
	MOVQ(litsTmp, litsPtrSlot)

	seqTmp := GP64()
	Load(ctx.Field("seqs"), seqTmp)
	MOVQ(seqTmp, seqsBaseSlot)
	LEAQ(Mem{Base: seqTmp, Disp: 32}, tmp)
	MOVQ(tmp, seqs3Slot)
	Load(ctx.Field("seqsLen"), tmp)
	SHLQ(Imm(4), tmp)
	ADDQ(tmp, seqTmp)
	MOVQ(seqTmp, seqPtrSlot)

	MOVQ(U64(prime8bytes), tmp)
	MOVQ(tmp, primeLSlot)
	MOVQ(U64(prime4bytes), tmp)
	MOVQ(tmp, primeSSlot)

	// bestLongLen=8, bestLongTableBits=22 -> (val * prime8bytes) >> 42
	hashL := func(val, out reg.GPVirtual) {
		MOVQ(val, out)
		IMULQ(primeLSlot, out)
		SHRQ(Imm(42), out)
	}
	// bestShortLen=4, bestShortTableBits=18 -> (uint32(val) * prime4bytes) >> 14
	hashS := func(val, out reg.GPVirtual) {
		MOVQ(val, out)
		IMULQ(primeSSlot, out)
		SHLQ(Imm(32), out)
		SHRQ(Imm(46), out)
	}

	// Quick filter: if 0 < candS - off < maxOff && load32(src, off) == first32, push to *qPtr.
	pushCand := func(off, candS, first32 reg.GPVirtual, rep int32, qPtr reg.GPVirtual) {
		lSkip := h.lbl("skip_cand")
		d := GP64()
		MOVQ(candS, d)
		SUBQ(off, d)
		TESTQ(d, d)
		JLE(LabelRef(lSkip))
		CMPQ(d, maxOffSlot)
		JGE(LabelRef(lSkip))
		t32 := GP64()
		MOVL(Mem{Base: src, Index: off, Scale: 1}, t32.As32())
		CMPL(t32.As32(), first32.As32())
		JNE(LabelRef(lSkip))
		MOVL(off.As32(), Mem{Base: qPtr, Disp: 0})
		MOVL(candS.As32(), Mem{Base: qPtr, Disp: 4})
		MOVL(U32(uint32(rep)), Mem{Base: qPtr, Disp: 8})
		ADDQ(Imm(16), qPtr)
		Label(lSkip)
	}

	dispatchBatch := func(qPtr reg.GPVirtual, stage int64, doneLbl string) {
		base := GP64()
		LEAQ(candBuf, base)
		CMPQ(qPtr, base)
		JE(LabelRef(doneLbl))
		MOVQ(base, qCurSlot)
		MOVQ(qPtr, qEndSlot)
		st := GP64()
		MOVQ(U64(uint64(stage)), st)
		MOVQ(st, stageSlot)
		JMP(LabelRef("improve_batch"))
		Label(doneLbl)
	}

	emitIndexLoop := func(endDisp int) {
		idx := GP64()
		MOVQ(index0Slot, idx)
		end := GP64()
		MOVQ(sLimitSlot, end)
		if endDisp > 0 {
			ADDQ(Imm(uint64(endDisp)), end)
		} else if endDisp < 0 {
			SUBQ(Imm(uint64(-endDisp)), end)
		}
		lMinOk := h.lbl("idx_minok")
		CMPQ(s, end)
		JGE(LabelRef(lMinOk))
		MOVQ(s, end)
		Label(lMinOk)

		lLoop := h.lbl("idx_loop")
		lDone := h.lbl("idx_done")
		CMPQ(idx, end)
		JGE(LabelRef(lDone))

		off := GP64()
		MOVQ(idx, off)
		ADDQ(curSlot, off)

		Label(lLoop)
		cv0 := GP64()
		MOVQ(Mem{Base: src, Index: idx, Scale: 1}, cv0)
		hh := GP64()
		hashL(cv0, hh)
		old := GP64()
		MOVQ(Mem{Base: longTab, Index: hh, Scale: 8}, old)
		nw := GP64()
		packPrevEntry(off, old, nw)
		MOVQ(nw, Mem{Base: longTab, Index: hh, Scale: 8})

		hashS(cv0, hh)
		MOVQ(Mem{Base: tab, Index: hh, Scale: 8}, old)
		packPrevEntry(off, old, nw)
		MOVQ(nw, Mem{Base: tab, Index: hh, Scale: 8})

		INCQ(off)
		INCQ(idx)
		CMPQ(idx, end)
		JL(LabelRef(lLoop))

		Label(lDone)
	}

	MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)

	PCALIGN(Imm(32))
	Label("encode_loop")
	{
		hL := GP64()
		hS := GP64()
		hashL(cv, hL)
		hashS(cv, hS)
		MOVQ(hL, hLSlot)
		MOVQ(hS, hSSlot)

		cL := GP64()
		cS := GP64()
		MOVQ(Mem{Base: longTab, Index: hL, Scale: 8}, cL)
		MOVQ(Mem{Base: tab, Index: hS, Scale: 8}, cS)
		MOVQ(cL, candLSlot)
		MOVQ(cS, candSSlot)

		// Prefetch next longTable entry (32 MB table -> hides DRAM miss for both miss & lazy s+1!).
		ns := GP64()
		MOVQ(s, ns)
		SUBQ(nextEmit, ns)
		SHRQ(Imm(9), ns)
		LEAQ(Mem{Base: s, Index: ns, Scale: 1, Disp: 1}, ns)
		CMPQ(ns, sLimitSlot)
		JGE(LabelRef("no_pf"))
		ncv := GP64()
		MOVQ(Mem{Base: src, Index: ns, Scale: 1}, ncv)
		nh := GP64()
		hashL(ncv, nh)
		PREFETCHT0(Mem{Base: longTab, Index: nh, Scale: 8})
		Label("no_pf")

		// Init best = {s: s, length: 0, rep: 0, est: highScore}
		z := GP64()
		XORQ(z, z)
		MOVQ(s, bestSSlot)
		MOVQ(z, bestLenSlot)
		MOVQ(z, bestRepSlot)
		MOVQ(z, bestOffSlot)
		hs := GP64()
		MOVQ(U64(highScore), hs)
		MOVQ(hs, bestEstSlot)

		// Stage 0: evaluate candL.offset, candL.prev, candS.offset, candS.prev at (s, uint32(cv), -1).
		qPtr := GP64()
		LEAQ(candBuf, qPtr)
		off := GP64()
		MOVLQSX(cL.As32(), off)
		SUBQ(curSlot, off)
		pushCand(off, s, cv, -1, qPtr)

		MOVQ(cL, off)
		SARQ(Imm(32), off)
		SUBQ(curSlot, off)
		pushCand(off, s, cv, -1, qPtr)

		MOVLQSX(cS.As32(), off)
		SUBQ(curSlot, off)
		pushCand(off, s, cv, -1, qPtr)

		MOVQ(cS, off)
		SARQ(Imm(32), off)
		SUBQ(curSlot, off)
		pushCand(off, s, cv, -1, qPtr)

		dispatchBatch(qPtr, 0, "stage0_done")
	}

	// Repeat checks if len(sequences) > 2 && best.length < 250.
	{
		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		CMPQ(sp, seqs3Slot)
		JBE(LabelRef("repeats_done"))
		CMPQ(bestLenSlot, Imm(250))
		JGE(LabelRef("repeats_done"))

		// Stage 1: s == nextEmit -> rep 1|4, 2|4, 3|4 at s.
		CMPQ(s, nextEmit)
		JNE(LabelRef("stage1_done"))
		{
			qPtr := GP64()
			LEAQ(candBuf, qPtr)
			off := GP64()
			MOVQ(s, off)
			SUBQ(offset2Slot, off)
			pushCand(off, s, cv, 1|4, qPtr)

			MOVQ(s, off)
			SUBQ(offset3Slot, off)
			pushCand(off, s, cv, 2|4, qPtr)

			o1 := GP64()
			MOVQ(offset1Slot, o1)
			CMPQ(o1, Imm(1))
			JLE(LabelRef("rep_no_o1m1"))
			MOVQ(s, off)
			SUBQ(o1, off)
			INCQ(off)
			pushCand(off, s, cv, 3|4, qPtr)
			Label("rep_no_o1m1")

			dispatchBatch(qPtr, 1, "stage1_done")
		}

		// Stage 2: best.rep <= 0 -> check repeats at s+1.
		CMPQ(bestRepSlot, Imm(0))
		JG(LabelRef("repeats_done"))
		{
			qPtr := GP64()
			LEAQ(candBuf, qPtr)
			spp := GP64()
			LEAQ(Mem{Base: s, Disp: 1}, spp)
			cv32 := GP64()
			MOVQ(cv, cv32)
			SHRQ(Imm(8), cv32)
			off := GP64()

			MOVQ(spp, off)
			SUBQ(offset1Slot, off)
			pushCand(off, spp, cv32, 1, qPtr)

			MOVQ(spp, off)
			SUBQ(offset2Slot, off)
			pushCand(off, spp, cv32, 2, qPtr)

			MOVQ(spp, off)
			SUBQ(offset3Slot, off)
			pushCand(off, spp, cv32, 3, qPtr)

			dispatchBatch(qPtr, 2, "stage2_done")
		}

		// Stage 3: best.rep < 0 -> check repeats at s+3.
		CMPQ(bestRepSlot, Imm(0))
		JGE(LabelRef("repeats_done"))
		{
			qPtr := GP64()
			LEAQ(candBuf, qPtr)
			spp := GP64()
			LEAQ(Mem{Base: s, Disp: 3}, spp)
			cv32 := GP64()
			MOVQ(cv, cv32)
			SHRQ(Imm(24), cv32)
			off := GP64()

			MOVQ(spp, off)
			SUBQ(offset1Slot, off)
			pushCand(off, spp, cv32, 1, qPtr)

			MOVQ(spp, off)
			SUBQ(offset2Slot, off)
			pushCand(off, spp, cv32, 2, qPtr)

			MOVQ(spp, off)
			SUBQ(offset3Slot, off)
			pushCand(off, spp, cv32, 3, qPtr)

			dispatchBatch(qPtr, 3, "stage3_done")
		}
	}
	Label("repeats_done")

	// Update longTable[hL] and table[hS], save index0 = s + 1.
	{
		sCur := GP64()
		MOVQ(s, sCur)
		ADDQ(curSlot, sCur)
		hh := GP64()
		old := GP64()
		nw := GP64()

		MOVQ(hLSlot, hh)
		MOVQ(candLSlot, old)
		packPrevEntry(sCur, old, nw)
		MOVQ(nw, Mem{Base: longTab, Index: hh, Scale: 8})

		MOVQ(hSSlot, hh)
		MOVQ(candSSlot, old)
		packPrevEntry(sCur, old, nw)
		MOVQ(nw, Mem{Base: tab, Index: hh, Scale: 8})

		idx0 := GP64()
		LEAQ(Mem{Base: s, Disp: 1}, idx0)
		MOVQ(idx0, index0Slot)
	}

	// Look ahead unless best.length >= 250.
	CMPQ(bestLenSlot, Imm(250))
	JGE(LabelRef("emit_match"))

	// If best.length < 4: no match, step forward!
	CMPQ(bestLenSlot, Imm(4))
	JGE(LabelRef("do_lazy"))
	{
		step := GP64()
		MOVQ(s, step)
		SUBQ(nextEmit, step)
		SHRQ(Imm(9), step)
		LEAQ(Mem{Base: s, Index: step, Scale: 1, Disp: 1}, s)
		CMPQ(s, sLimitSlot)
		JGE(LabelRef("done"))
		MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)
		JMP(LabelRef("encode_loop"))
	}

	Label("do_lazy")
	{
		// Stage 4: short at s+1, long at s+1 & s+2.
		// Spill cS1/cL1/cL2 into dead candSSlot/candLSlot/candEndSlot to keep register pressure low.
		cvSh := GP64()
		MOVQ(cv, cvSh)
		SHRQ(Imm(8), cvSh)
		hS1 := GP64()
		hashS(cvSh, hS1)
		cS1 := GP64()
		MOVQ(Mem{Base: tab, Index: hS1, Scale: 8}, cS1)
		MOVQ(cS1, candSSlot)

		MOVQ(Mem{Base: src, Index: s, Scale: 1, Disp: 1}, cv) // cv = load64(src, s+1)
		cv2 := GP64()
		MOVQ(Mem{Base: src, Index: s, Scale: 1, Disp: 2}, cv2)

		hL1 := GP64()
		hashL(cv, hL1)
		cL1 := GP64()
		MOVQ(Mem{Base: longTab, Index: hL1, Scale: 8}, cL1)
		MOVQ(cL1, candLSlot)

		hL2 := GP64()
		hashL(cv2, hL2)
		cL2 := GP64()
		MOVQ(Mem{Base: longTab, Index: hL2, Scale: 8}, cL2)
		MOVQ(cL2, candEndSlot)

		qPtr := GP64()
		LEAQ(candBuf, qPtr)
		s1 := GP64()
		LEAQ(Mem{Base: s, Disp: 1}, s1)
		off := GP64()

		MOVQ(candSSlot, off)
		MOVLQSX(off.As32(), off)
		SUBQ(curSlot, off)
		pushCand(off, s1, cv, -1, qPtr)

		MOVQ(candLSlot, off)
		MOVLQSX(off.As32(), off)
		SUBQ(curSlot, off)
		pushCand(off, s1, cv, -1, qPtr)

		MOVQ(candLSlot, off)
		SARQ(Imm(32), off)
		SUBQ(curSlot, off)
		pushCand(off, s1, cv, -1, qPtr)

		INCQ(s1) // s+2
		MOVQ(candEndSlot, off)
		MOVLQSX(off.As32(), off)
		SUBQ(curSlot, off)
		pushCand(off, s1, cv2, -1, qPtr)

		MOVQ(candEndSlot, off)
		SARQ(Imm(32), off)
		SUBQ(curSlot, off)
		pushCand(off, s1, cv2, -1, qPtr)

		dispatchBatch(qPtr, 4, "stage4_done")
	}

	// Stage 5 & 6: end-of-match long check (skipBeginning = 2).
	{
		sMin2 := GP64()
		LEAQ(Mem{Base: s, Disp: -2}, sMin2)
		CMPQ(bestSSlot, sMin2)
		JLE(LabelRef("emit_match"))

		sAt := GP64()
		MOVQ(bestSSlot, sAt)
		ADDQ(bestLenSlot, sAt)
		CMPQ(sAt, sLimitSlot)
		JGE(LabelRef("emit_match"))

		cvEnd := GP64()
		MOVQ(Mem{Base: src, Index: sAt, Scale: 1}, cvEnd)
		hLEnd := GP64()
		hashL(cvEnd, hLEnd)
		cEnd := GP64()
		MOVQ(Mem{Base: longTab, Index: hLEnd, Scale: 8}, cEnd)
		MOVQ(cEnd, candEndSlot)

		// off = cEnd.offset - cur - best.length + 2
		off := GP64()
		MOVLQSX(cEnd.As32(), off)
		SUBQ(curSlot, off)
		SUBQ(bestLenSlot, off)
		ADDQ(Imm(2), off)
		TESTQ(off, off)
		JS(LabelRef("emit_match"))

		qPtr := GP64()
		LEAQ(candBuf, qPtr)
		bs2 := GP64()
		MOVQ(bestSSlot, bs2)
		ADDQ(Imm(2), bs2)
		f32 := GP64()
		MOVL(Mem{Base: src, Index: bs2, Scale: 1}, f32.As32())
		pushCand(off, bs2, f32, -1, qPtr)
		dispatchBatch(qPtr, 5, "stage5_done")

		// Stage 6: cEnd.prev - cur - best.length + 2 (using possibly updated best.length & best.s!)
		off2 := GP64()
		MOVQ(candEndSlot, off2)
		SARQ(Imm(32), off2)
		SUBQ(curSlot, off2)
		SUBQ(bestLenSlot, off2)
		ADDQ(Imm(2), off2)
		TESTQ(off2, off2)
		JS(LabelRef("emit_match"))

		qPtr2 := GP64()
		LEAQ(candBuf, qPtr2)
		bs2b := GP64()
		MOVQ(bestSSlot, bs2b)
		ADDQ(Imm(2), bs2b)
		f32b := GP64()
		MOVL(Mem{Base: src, Index: bs2b, Scale: 1}, f32b.As32())
		pushCand(off2, bs2b, f32b, -1, qPtr2)
		dispatchBatch(qPtr2, 6, "stage6_done")
	}

	// Emit winning match!
	Label("emit_match")
	MOVQ(bestSSlot, s)
	CMPQ(bestRepSlot, Imm(0))
	JLE(LabelRef("emit_normal"))

	// Repeat match emission.
	{
		litLen := GP64()
		h.copyLits(src, nextEmit, s, litLen, litsPtrSlot)

		mlEmit := GP64()
		MOVQ(bestLenSlot, mlEmit)
		SUBQ(Imm(zstdMinMatch), mlEmit)
		repCode := GP64()
		MOVQ(bestRepSlot, repCode)
		ANDQ(Imm(3), repCode)

		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		MOVL(litLen.As32(), Mem{Base: sp, Disp: 0})
		MOVL(mlEmit.As32(), Mem{Base: sp, Disp: 4})
		MOVL(repCode.As32(), Mem{Base: sp, Disp: 8})
		ADDQ(Imm(16), sp)
		MOVQ(sp, seqPtrSlot)

		ADDQ(bestLenSlot, s)
		MOVQ(s, nextEmit)

		emitIndexLoop(4) // end = min(s, sLimit+4)

		// Rotate repeat offsets according to best.rep.
		rep := GP64()
		MOVQ(bestRepSlot, rep)
		CMPQ(rep, Imm(1))
		JE(LabelRef("rep_rot_done"))
		CMPQ(rep, Imm(2))
		JE(LabelRef("rep_rot_12"))
		CMPQ(rep, Imm(5))
		JE(LabelRef("rep_rot_12"))
		CMPQ(rep, Imm(7))
		JE(LabelRef("rep_rot_1m1"))
		// case 3, 6: offset1, offset2, offset3 = offset3, offset1, offset2
		o1 := GP64()
		o2 := GP64()
		o3 := GP64()
		MOVQ(offset1Slot, o1)
		MOVQ(offset2Slot, o2)
		MOVQ(offset3Slot, o3)
		MOVQ(o3, offset1Slot)
		MOVQ(o1, offset2Slot)
		MOVQ(o2, offset3Slot)
		JMP(LabelRef("rep_rot_done"))

		Label("rep_rot_12")
		MOVQ(offset1Slot, o1)
		MOVQ(offset2Slot, o2)
		MOVQ(o2, offset1Slot)
		MOVQ(o1, offset2Slot)
		JMP(LabelRef("rep_rot_done"))

		Label("rep_rot_1m1")
		MOVQ(offset1Slot, o1)
		MOVQ(offset2Slot, o2)
		LEAQ(Mem{Base: o1, Disp: -1}, o3)
		MOVQ(o3, offset1Slot)
		MOVQ(o1, offset2Slot)
		MOVQ(o2, offset3Slot)

		Label("rep_rot_done")
		CMPQ(s, sLimitSlot)
		JGE(LabelRef("done"))
		MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)
		JMP(LabelRef("encode_loop"))
	}

	// Normal match emission.
	Label("emit_normal")
	{
		tOff := GP64()
		MOVQ(bestOffSlot, tOff)
		newOff := GP64()
		MOVQ(s, newOff)
		SUBQ(tOff, newOff)

		o1 := GP64()
		o2 := GP64()
		MOVQ(offset1Slot, o1)
		MOVQ(offset2Slot, o2)
		MOVQ(newOff, offset1Slot)
		MOVQ(o1, offset2Slot)
		MOVQ(o2, offset3Slot)

		litLen := GP64()
		h.copyLits(src, nextEmit, s, litLen, litsPtrSlot)

		mlEmit := GP64()
		MOVQ(bestLenSlot, mlEmit)
		SUBQ(Imm(zstdMinMatch), mlEmit)
		offEmit := GP64()
		LEAQ(Mem{Base: newOff, Disp: 3}, offEmit)

		sp := GP64()
		MOVQ(seqPtrSlot, sp)
		MOVL(litLen.As32(), Mem{Base: sp, Disp: 0})
		MOVL(mlEmit.As32(), Mem{Base: sp, Disp: 4})
		MOVL(offEmit.As32(), Mem{Base: sp, Disp: 8})
		ADDQ(Imm(16), sp)
		MOVQ(sp, seqPtrSlot)

		ADDQ(bestLenSlot, s)
		MOVQ(s, nextEmit)

		emitIndexLoop(-4) // end = min(s, sLimit-4)

		CMPQ(s, sLimitSlot)
		JGE(LabelRef("done"))
		MOVQ(Mem{Base: src, Index: s, Scale: 1}, cv)
		JMP(LabelRef("encode_loop"))
	}

	// Shared improveSlow batch processor (ONE copy for all 7 stages!).
	PCALIGN(Imm(32))
	Label("improve_batch")
	{
		q := GP64()
		MOVQ(qCurSlot, q)
		cOff := GP64()
		cS := GP64()
		cRep := GP64()
		MOVLQSX(Mem{Base: q, Disp: 0}, cOff)
		MOVLQSX(Mem{Base: q, Disp: 4}, cS)
		MOVLQSX(Mem{Base: q, Disp: 8}, cRep)
		ADDQ(Imm(16), q)
		MOVQ(q, qCurSlot)

		// Quick reject if best.length > 16 (enc_best.go:425-438).
		bLen := GP64()
		MOVQ(bestLenSlot, bLen)
		CMPQ(bLen, Imm(16))
		JLE(LabelRef("imp_ml"))

		left := GP64()
		MOVQ(srcLenSlot, left)
		SUBQ(bestSSlot, left)
		SUBQ(bLen, left)
		TESTQ(left, left)
		JLE(LabelRef("imp_next"))

		// checkLen = best.length - (cS - best.s) - 8
		SUBQ(cS, bLen)
		ADDQ(bestSSlot, bLen)
		SUBQ(Imm(8), bLen)
		CMPQ(left, Imm(2))
		JLE(LabelRef("imp_ml"))
		CMPQ(bLen, Imm(4))
		JLE(LabelRef("imp_ml"))

		MOVQ(cOff, left)
		ADDQ(bLen, left)
		MOVL(Mem{Base: src, Index: left, Scale: 1}, left.As32())
		ADDQ(cS, bLen)
		MOVL(Mem{Base: src, Index: bLen, Scale: 1}, bLen.As32())
		CMPL(left.As32(), bLen.As32())
		JNE(LabelRef("imp_next"))

		Label("imp_ml")
		// cOff, cS, cRep are already stored at [qCurSlot-16]; reload them after matchLen.
		aOff := GP64()
		bOff := GP64()
		LEAQ(Mem{Base: cS, Disp: 4}, aOff)
		LEAQ(Mem{Base: cOff, Disp: 4}, bOff)
		ml := GP64()
		h.matchLenPtr(src, aOff, bOff, ml, srcLenSlot)
		ADDQ(Imm(4), ml)

		// Reload cOff, cS, cRep from [qCurSlot - 16].
		qPrev := GP64()
		MOVQ(qCurSlot, qPrev)
		MOVLQSX(Mem{Base: qPrev, Disp: -16}, cOff)
		MOVLQSX(Mem{Base: qPrev, Disp: -12}, cS)
		MOVLQSX(Mem{Base: qPrev, Disp: -8}, cRep)

		// Backward extension if best.rep <= 0.
		CMPQ(bestRepSlot, Imm(0))
		JG(LabelRef("imp_back_done"))
		tMin := GP64()
		MOVQ(cS, tMin)
		SUBQ(maxOffSlot, tMin)
		JNS(LabelRef("imp_tmin_ok"))
		XORQ(tMin, tMin)
		Label("imp_tmin_ok")

		Label("imp_back_loop")
		CMPQ(cOff, tMin)
		JLE(LabelRef("imp_back_done"))
		CMPQ(cS, nextEmit)
		JLE(LabelRef("imp_back_done"))
		CMPQ(ml, Imm(maxMatchLength))
		JAE(LabelRef("imp_back_done"))
		b1 := GP64()
		b2 := GP64()
		MOVBQZX(Mem{Base: src, Index: cOff, Scale: 1, Disp: -1}, b1)
		MOVBQZX(Mem{Base: src, Index: cS, Scale: 1, Disp: -1}, b2)
		CMPQ(b1, b2)
		JNE(LabelRef("imp_back_done"))
		DECQ(cS)
		DECQ(cOff)
		INCQ(ml)
		JMP(LabelRef("imp_back_loop"))

		Label("imp_back_done")
		// estBits(cand, bitsPerByte) using only 3 scratch regs (est, t1, t2).
		est := GP64()
		t1 := GP64()
		t2 := GP64()

		// mlCost -> est
		LEAQ(Mem{Base: ml, Disp: -zstdMinMatch}, t1)
		CMPQ(t1, Imm(127))
		JA(LabelRef("imp_ml_big"))
		MOVQ(predefMLDirectSlot, t2)
		MOVLQSX(Mem{Base: t2, Index: t1, Scale: 4}, est)
		JMP(LabelRef("imp_ml_cost_ok"))
		Label("imp_ml_big")
		BSRQ(t1, t1)
		ADDQ(Imm(36), t1)
		MOVQ(predefMLCostSlot, t2)
		MOVLQSX(Mem{Base: t2, Index: t1, Scale: 4}, est)

		Label("imp_ml_cost_ok")
		// ofCost -> add to est
		TESTQ(cRep, cRep)
		JS(LabelRef("imp_off_norm"))
		MOVQ(cRep, t1)
		ANDQ(Imm(3), t1)
		JMP(LabelRef("imp_of_cost"))
		Label("imp_off_norm")
		MOVQ(cS, t1)
		SUBQ(cOff, t1)
		ADDQ(Imm(3), t1)
		Label("imp_of_cost")
		BSRQ(t1, t1)
		MOVQ(predefOFSlot, t2)
		MOVLQSX(Mem{Base: t2, Index: t1, Scale: 4}, t1)
		ADDQ(t1, est)

		// est = ofCost + mlCost - ((ml * bpb) >> 10)
		MOVQ(ml, t1)
		IMULQ(bpbSlot, t1)
		SARQ(Imm(10), t1)
		SUBQ(t1, est)

		TESTQ(est, est)
		JLE(LabelRef("imp_est_ok"))
		XORQ(ml, ml)
		MOVQ(U64(highScore), est)

		Label("imp_est_ok")
		// Accept if best.est >= highScore || est - best.est + ((cS - best.s)*bpb >> 10) < 0
		CMPQ(bestEstSlot, Imm(highScore))
		JGE(LabelRef("imp_accept"))

		MOVQ(cS, t1)
		SUBQ(bestSSlot, t1)
		IMULQ(bpbSlot, t1)
		SARQ(Imm(10), t1)
		ADDQ(est, t1)
		SUBQ(bestEstSlot, t1)
		TESTQ(t1, t1)
		JGE(LabelRef("imp_next"))

		Label("imp_accept")
		MOVQ(cOff, bestOffSlot)
		MOVQ(cS, bestSSlot)
		MOVQ(ml, bestLenSlot)
		MOVQ(cRep, bestRepSlot)
		MOVQ(est, bestEstSlot)

		Label("imp_next")
		q2 := GP64()
		MOVQ(qCurSlot, q2)
		CMPQ(q2, qEndSlot)
		JL(LabelRef("improve_batch"))

		// Return to caller stage.
		st := GP64()
		MOVQ(stageSlot, st)
		CMPQ(st, Imm(0))
		JE(LabelRef("stage0_done"))
		CMPQ(st, Imm(1))
		JE(LabelRef("stage1_done"))
		CMPQ(st, Imm(2))
		JE(LabelRef("stage2_done"))
		CMPQ(st, Imm(3))
		JE(LabelRef("stage3_done"))
		CMPQ(st, Imm(4))
		JE(LabelRef("stage4_done"))
		CMPQ(st, Imm(5))
		JE(LabelRef("stage5_done"))
		JMP(LabelRef("stage6_done"))
	}

	Label("done")
	litsEnd := GP64()
	MOVQ(litsPtrSlot, litsEnd)
	SUBQ(litsBaseSlot, litsEnd)
	Store(litsEnd, ctx.Field("litsLen"))

	seqsEnd := GP64()
	MOVQ(seqPtrSlot, seqsEnd)
	SUBQ(seqsBaseSlot, seqsEnd)
	SHRQ(Imm(4), seqsEnd)
	Store(seqsEnd, ctx.Field("seqsLen"))

	outTmp := GP64()
	MOVQ(offset1Slot, outTmp)
	Store(outTmp, ctx.Field("offset1"))
	MOVQ(offset2Slot, outTmp)
	Store(outTmp, ctx.Field("offset2"))
	MOVQ(offset3Slot, outTmp)
	Store(outTmp, ctx.Field("offset3"))
	Store(nextEmit, ctx.Field("nextEmit"))

	RET()
}
