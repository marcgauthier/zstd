package main

//go:generate go run gen_enc.go -out ../compress.s -arch amd64,arm64 -arm64-promote-stack-slots -pkg=huff0

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

	genCompress1xDo("compress1xDo_amd64", false)
	genCompress1xDo("compress1xDo_bmi2", true)
	genCountSimple4("countSimple4_asm")

	Generate()
}

func genCompress1xDo(name string, bmi2 bool) {
	Package("github.com/klauspost/compress/huff0")
	TEXT(name, 0, "func(ctx *compress1xDoCtx)")
	Doc(name+" encodes 4-byte groups backwards into a Huffman bitstream using tree reduction.", "")
	Pragma("noescape")

	ctx := Dereference(Param("ctx"))

	c := GP64()
	nb := GP64()
	dstPtr := GP64()
	srcPtr := GP64()
	count := GP64()
	ctPtr := GP64()
	dstBaseSlot := AllocLocal(8)

	Load(ctx.Field("c"), c)
	Load(ctx.Field("nb"), nb)
	Load(ctx.Field("ct"), ctPtr)
	Load(ctx.Field("count"), count)

	tmp := GP64()
	Load(ctx.Field("buf"), dstPtr)
	MOVQ(dstPtr, dstBaseSlot)
	Load(ctx.Field("pos"), tmp)
	ADDQ(tmp, dstPtr)

	Load(ctx.Field("src"), srcPtr)
	Load(ctx.Field("startN"), tmp)
	ADDQ(tmp, srcPtr)

	shl := func(val, shift reg.GPVirtual) {
		if bmi2 {
			SHLXQ(shift, val, val)
		} else {
			MOVQ(shift, reg.RCX)
			SHLQ(reg.CL, val)
		}
	}

	loadPair := func(dispHi, dispLo int, outV, outN reg.GPVirtual) {
		bHi := GP64()
		bLo := GP64()
		eHi := GP64()
		eLo := GP64()
		vLo := GP64()

		MOVBQZX(Mem{Base: srcPtr, Disp: dispHi}, bHi)
		MOVBQZX(Mem{Base: srcPtr, Disp: dispLo}, bLo)
		MOVL(Mem{Base: ctPtr, Index: bHi, Scale: 4}, eHi.As32())
		MOVL(Mem{Base: ctPtr, Index: bLo, Scale: 4}, eLo.As32())

		MOVWQZX(eHi.As16(), outV)
		SHRQ(Imm(16), eHi)
		ANDQ(Imm(0xff), eHi) // nHi

		MOVWQZX(eLo.As16(), vLo)
		SHRQ(Imm(16), eLo)
		ANDQ(Imm(0xff), eLo) // nLo

		shl(vLo, eHi)
		ORQ(vLo, outV)
		LEAQ(Mem{Base: eHi, Index: eLo, Scale: 1}, outN)
	}

	PCALIGN(Imm(32))
	Label("loop")

	// Flush whole bytes from c into dstPtr (leaves nb <= 7).
	{
		t := GP64()
		MOVQ(c, Mem{Base: dstPtr})
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

	// Tree-reduce 4 symbols: src[3], src[2], src[1], src[0].
	v32 := GP64()
	n32 := GP64()
	v10 := GP64()
	n10 := GP64()

	loadPair(3, 2, v32, n32)
	loadPair(1, 0, v10, n10)
	SUBQ(Imm(4), srcPtr)

	shl(v10, n32)
	ORQ(v10, v32)
	ADDQ(n10, n32)

	shl(v32, nb)
	ORQ(v32, c)
	ADDQ(n32, nb)

	DECQ(count)
	JNZ(LabelRef("loop"))

	Store(c, ctx.Field("c"))
	Store(nb, ctx.Field("nb"))
	MOVQ(dstBaseSlot, tmp)
	SUBQ(tmp, dstPtr)
	Store(dstPtr, ctx.Field("pos"))

	RET()
}

func genCountSimple4(name string) {
	Package("github.com/klauspost/compress/huff0")
	TEXT(name, 0, "func(src *byte, n int64, cnt0, cnt1, cnt2, cnt3 *uint32)")
	Doc(name+" builds a 4-way unrolled byte histogram in cnt0..cnt3.", "")
	Pragma("noescape")

	src := GP64()
	n := GP64()
	cnt0 := GP64()
	cnt1 := GP64()
	cnt2 := GP64()
	cnt3 := GP64()

	Load(Param("src"), src)
	Load(Param("n"), n)
	Load(Param("cnt0"), cnt0)
	Load(Param("cnt1"), cnt1)
	Load(Param("cnt2"), cnt2)
	Load(Param("cnt3"), cnt3)

	b0 := GP64()
	b1 := GP64()
	b2 := GP64()
	b3 := GP64()
	t0 := GP64()
	t1 := GP64()
	t2 := GP64()
	t3 := GP64()

	CMPQ(n, Imm(8))
	JB(LabelRef("tail"))

	step4 := func(baseDisp int) {
		MOVBQZX(Mem{Base: src, Disp: baseDisp + 0}, b0)
		MOVBQZX(Mem{Base: src, Disp: baseDisp + 1}, b1)
		MOVBQZX(Mem{Base: src, Disp: baseDisp + 2}, b2)
		MOVBQZX(Mem{Base: src, Disp: baseDisp + 3}, b3)

		MOVL(Mem{Base: cnt0, Index: b0, Scale: 4}, t0.As32())
		MOVL(Mem{Base: cnt1, Index: b1, Scale: 4}, t1.As32())
		MOVL(Mem{Base: cnt2, Index: b2, Scale: 4}, t2.As32())
		MOVL(Mem{Base: cnt3, Index: b3, Scale: 4}, t3.As32())

		INCQ(t0)
		INCQ(t1)
		INCQ(t2)
		INCQ(t3)

		MOVL(t0.As32(), Mem{Base: cnt0, Index: b0, Scale: 4})
		MOVL(t1.As32(), Mem{Base: cnt1, Index: b1, Scale: 4})
		MOVL(t2.As32(), Mem{Base: cnt2, Index: b2, Scale: 4})
		MOVL(t3.As32(), Mem{Base: cnt3, Index: b3, Scale: 4})
	}

	PCALIGN(Imm(32))
	Label("loop8")
	step4(0)
	step4(4)
	ADDQ(Imm(8), src)
	SUBQ(Imm(8), n)
	CMPQ(n, Imm(8))
	JAE(LabelRef("loop8"))

	Label("tail")
	TESTQ(n, n)
	JZ(LabelRef("done"))

	Label("tail1")
	MOVBQZX(Mem{Base: src}, b0)
	INCQ(src)
	MOVL(Mem{Base: cnt0, Index: b0, Scale: 4}, t0.As32())
	INCQ(t0)
	MOVL(t0.As32(), Mem{Base: cnt0, Index: b0, Scale: 4})
	DECQ(n)
	JNZ(LabelRef("tail1"))

	Label("done")
	RET()
}
