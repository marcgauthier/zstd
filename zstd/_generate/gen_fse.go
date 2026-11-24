package main

//go:generate go run gen_fse.go -out ../fse_decoder.s -arch amd64,arm64 -arm64-promote-stack-slots -pkg=zstd

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

	buildDtable := buildDtable{}
	buildDtable.generateProcedure("buildDtable_asm")
	Generate()
}

const (
	errorCorruptedNormalizedCounter = 1
	errorNewStateTooBig             = 2
	errorNewStateNoBits             = 3
)

type buildDtable struct {
	bmi2 bool

	// values used across all methods
	actualTableLog reg.GPVirtual
	tableSize      reg.GPVirtual
	highThreshold  reg.GPVirtual
	symbolNext     reg.GPVirtual // array []uint16
	dt             reg.GPVirtual // array []uint64
	ext            reg.GPVirtual // array []uint64, per symbol
}

func (b *buildDtable) generateProcedure(name string) {
	Package("github.com/klauspost/compress/zstd")
	TEXT(name, 0, "func (s *fseDecoder, ctx *buildDtableAsmContext ) int")
	Doc(name+" implements fseDecoder.buildDtable in asm", "")
	Pragma("noescape")

	ctx := Dereference(Param("ctx"))
	s := Dereference(Param("s"))

	Comment("Load values")
	{
		// tableSize = (1 << s.actualTableLog)
		b.tableSize = GP64()
		b.actualTableLog = GP64()
		Load(s.Field("actualTableLog"), b.actualTableLog)
		XORQ(b.tableSize, b.tableSize)
		BTSQ(b.actualTableLog, b.tableSize)

		// symbolNext = &s.stateTable[0]
		b.symbolNext = GP64()
		Load(ctx.Field("stateTable"), b.symbolNext)

		// dt = &s.dt[0]
		b.dt = GP64()
		Load(ctx.Field("dt"), b.dt)

		// ext = &ext[0]
		b.ext = GP64()
		Load(ctx.Field("ext"), b.ext)

		// highThreshold = tableSize - 1
		b.highThreshold = GP64()
		LEAQ(Mem{Base: b.tableSize, Disp: -1}, b.highThreshold)
	}

	norm := GP64()
	Load(ctx.Field("norm"), norm)

	symbolLen := GP64()
	Load(s.Field("symbolLen"), symbolLen)
	Comment("End load values")

	b.init(norm, symbolLen)
	b.spread(norm, symbolLen)
	b.buildTable()

	b.returnCode(0)
}

func (b *buildDtable) init(norm, symbolLen reg.GPVirtual) {
	Comment("Init, lay down lowprob symbols")
	/*
		for i, v := range s.norm[:s.symbolLen] {
			if v == -1 {
				s.dt[highThreshold].setAddBits(uint8(i))
				highThreshold--
				symbolNext[i] = 1
			} else {
				symbolNext[i] = uint16(v)
			}
		}
	*/

	i := New64()
	JMP(LabelRef("init_main_loop_condition"))
	Label("init_main_loop")

	v := GP64()
	MOVWQSX(Mem{Base: norm, Index: i, Scale: 2}, v)

	CMPW(v.As16(), I16(-1))
	JNE(LabelRef("do_not_update_high_threshold"))

	{
		// s.dt[highThreshold].setAddBits(uint8(i))
		MOVB(i.As8(), Mem{Base: b.dt, Index: b.highThreshold, Scale: 8, Disp: 1}) // set highThreshold*8 + 1 byte
		// highThreshold--
		DECQ(b.highThreshold)

		// symbolNext[i] = 1
		MOVQ(U64(1), v)
	}

	Label("do_not_update_high_threshold")
	{
		// symbolNext[i] = uint16(v)
		MOVW(v.As16(), Mem{Base: b.symbolNext, Index: i, Scale: 2})

		INCQ(i)
		Label("init_main_loop_condition")
		CMPQ(i, symbolLen)
		JL(LabelRef("init_main_loop"))
	}

	Label("init_end")
}

func (b *buildDtable) spread(norm, symbolLen reg.GPVirtual) {
	Comment("Spread symbols")
	/*
		tableMask := tableSize - 1
		step := tableStep(tableSize)
		position := uint32(0)
		for ss, v := range s.norm[:s.symbolLen] {
			for i := 0; i < int(v); i++ {
				s.dt[position].setAddBits(uint8(ss))
				position = (position + step) & tableMask
				for position > highThreshold {
					// lowprob area
					position = (position + step) & tableMask
				}
			}
		}
	*/
	step := GP64()
	Comment("Calculate table step")
	{
		// tmp1 = tableSize >> 1
		tmp1 := Copy64(b.tableSize)
		SHRQ(U8(1), tmp1)

		// tmp3 = tableSize >> 3
		tmp3 := Copy64(b.tableSize)
		SHRQ(U8(3), tmp3)

		// step = tmp1 + tmp3 + 3
		LEAQ(Mem{Base: tmp1, Index: tmp3, Scale: 1, Disp: 3}, step)
	}

	Comment("Fill add bits values")

	// tableMask = tableSize - 1 (tableSize is a pow of 2)
	tableMask := GP64()
	LEAQ(Mem{Base: b.tableSize, Disp: -1}, tableMask)

	// position := 0
	position := New64()

	// ss := 0
	ss := New64()
	// Fast path when there are no lowprob (-1) symbols: highThreshold == tableMask,
	// so position never exceeds highThreshold and the inner check disappears.
	CMPQ(b.highThreshold, tableMask)
	JNE(LabelRef("spread_slow_loop_condition"))

	JMP(LabelRef("spread_fast_loop_condition"))
	Label("spread_fast_loop")
	{
		v := GP64()
		MOVWQSX(Mem{Base: norm, Index: ss, Scale: 2}, v)
		TESTQ(v, v)
		JLE(LabelRef("spread_fast_next"))
		Label("spread_fast_inner")
		MOVB(ss.As8(), Mem{Base: b.dt, Index: position, Scale: 8, Disp: 1})
		ADDQ(step, position)
		ANDQ(tableMask, position)
		DECQ(v)
		JNZ(LabelRef("spread_fast_inner"))
	}
	Label("spread_fast_next")
	INCQ(ss)
	Label("spread_fast_loop_condition")
	CMPQ(ss, symbolLen)
	JL(LabelRef("spread_fast_loop"))
	JMP(LabelRef("spread_done"))

	Label("spread_slow_loop")
	{
		v := GP64()
		MOVWQSX(Mem{Base: norm, Index: ss, Scale: 2}, v)
		TESTQ(v, v)
		JLE(LabelRef("spread_slow_next"))
		Label("spread_slow_inner")
		{
			// s.dt[position].setAddBits(uint8(ss))
			MOVB(ss.As8(), Mem{Base: b.dt, Index: position, Scale: 8, Disp: 1})

			Label("adjust_position")
			// position = (position + step) & tableMask
			ADDQ(step, position)
			ANDQ(tableMask, position)

			// for position > highThreshold {
			// 	// lowprob area
			// 	position = (position + step) & tableMask
			// }
			CMPQ(position, b.highThreshold)
			JG(LabelRef("adjust_position"))
		}
		DECQ(v)
		JNZ(LabelRef("spread_slow_inner"))
	}
	Label("spread_slow_next")
	INCQ(ss)
	Label("spread_slow_loop_condition")
	CMPQ(ss, symbolLen)
	JL(LabelRef("spread_slow_loop"))

	Label("spread_done")
	/*
		if position != 0 {
			// position must reach all cells once, otherwise normalizedCounter is incorrect
			return errors.New("corrupted input (position != 0)")
		}
	*/
	TESTQ(position, position)
	{
		JZ(LabelRef("spread_check_ok"))
		b.returnError(errorCorruptedNormalizedCounter, position)
	}
	Label("spread_check_ok")
}

func (b *buildDtable) buildTable() {
	Comment("Build Decoding table")
	u := New64()
	Label("build_table_main_table")
	{
		// v := s.dt[u]
		// symbol := v.addBits()
		symbol := GP64()
		MOVBQZX(Mem{Base: b.dt, Index: u, Scale: 8, Disp: 1}, symbol)

		// nextState := symbolNext[symbol]
		nextState := GP64()
		ptr := Mem{Base: b.symbolNext, Index: symbol, Scale: 2}
		MOVWQZX(ptr, nextState)

		// symbolNext[symbol] = nextState + 1
		{
			tmp := GP64()
			LEAQ(Mem{Base: nextState, Disp: 1}, tmp)
			MOVW(tmp.As16(), ptr)
		}

		// nBits := s.actualTableLog - byte(highBits(uint32(nextState)))
		nBits := reg.RCX // As we use nBits to shift
		{
			highBits := Copy64(nextState)
			BSRQ(highBits, highBits)

			MOVQ(b.actualTableLog, nBits)
			SUBQ(highBits, nBits)
		}

		// newState := (nextState << nBits) - tableSize
		newState := Copy64(nextState)
		SHLQ(reg.CL, newState)
		SUBQ(b.tableSize, newState)

		// if newState > tableSize (unsigned check also catches < 0)
		CMPQ(newState, b.tableSize)
		JA(LabelRef("build_table_err_newstate"))

		// if newState == uint16(u) && nBits == 0
		TESTB(nBits.As8(), nBits.As8())
		JZ(LabelRef("build_table_check_nobits"))

		Label("build_table_store")
		// s.dt[u&maxTableMask] = ext[symbol] | decSymbol(nBits) | decSymbol(newState)<<16
		// newState <= tableSize <= 512 so it fits cleanly in 16 bits without masking.
		{
			entry := GP64()
			MOVQ(Mem{Base: b.ext, Index: symbol, Scale: 8}, entry)
			ORQ(nBits, entry)
			SHLQ(U8(16), newState)
			ORQ(newState, entry)
			MOVQ(entry, Mem{Base: b.dt, Index: u, Scale: 8})
		}

		INCQ(u)
		CMPQ(u, b.tableSize)
		JL(LabelRef("build_table_main_table"))
		JMP(LabelRef("build_table_done"))

		Label("build_table_check_nobits")
		CMPW(newState.As16(), u.As16())
		JNE(LabelRef("build_table_store"))
		b.returnError(errorNewStateNoBits, newState, u)

		Label("build_table_err_newstate")
		b.returnError(errorNewStateTooBig, newState, b.tableSize)
	}
	Label("build_table_done")
}

// returnCode sets function result and terminates the function.
func (b *buildDtable) returnCode(code int) {
	a, err := ReturnIndex(0).Resolve()
	if err != nil {
		panic(err)
	}
	MOVQ(I32(code), a.Addr)
	RET()
}

// returnError sets error params and terminates function with given exit code.
func (b *buildDtable) returnError(code int, args ...reg.GPVirtual) {
	ctx := Dereference(Param("ctx"))

	if len(args) > 0 {
		Store(args[0], ctx.Field("errParam1"))
	}

	if len(args) > 1 {
		Store(args[1], ctx.Field("errParam2"))
	}

	b.returnCode(code)
}

func New64() reg.GPVirtual {
	cnt := GP64()
	XORQ(cnt, cnt)

	return cnt
}

func Copy64(val reg.GPVirtual) reg.GPVirtual {
	tmp := GP64()
	MOVQ(val, tmp)

	return tmp
}
