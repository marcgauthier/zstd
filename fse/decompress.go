package fse

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

const (
	tablelogAbsoluteMax = 15
)

// Decompress a block of data.
// You can provide a scratch buffer to avoid allocations.
// If nil is provided a temporary one will be allocated.
// It is possible, but by no way guaranteed that corrupt data will
// return an error.
// It is up to the caller to verify integrity of the returned data.
// Use a predefined Scratch to set maximum acceptable output size.
func Decompress(b []byte, s *Scratch) ([]byte, error) {
	s, err := s.prepare(b)
	if err != nil {
		return nil, err
	}
	defer s.cleanup()
	s.Out = s.Out[:0]
	err = s.readNCount()
	if err != nil {
		return nil, err
	}
	err = s.buildDtable()
	if err != nil {
		return nil, err
	}
	err = s.decompress()
	if err != nil {
		return nil, err
	}

	return s.Out, nil
}

// readNCount will read the symbol distribution so decoding tables can be constructed.
func (s *Scratch) readNCount() error {
	var (
		charnum   uint16
		previous0 bool
		b         = &s.br
	)
	iend := b.remain()
	if iend < 4 {
		return errors.New("input too small")
	}
	bitStream := b.Uint32()
	nbBits := uint((bitStream & 0xF) + minTablelog) // extract tableLog
	if nbBits > tablelogAbsoluteMax {
		return errors.New("tableLog too large")
	}
	bitStream >>= 4
	bitCount := uint(4)

	s.actualTableLog = uint8(nbBits)
	remaining := int32((1 << nbBits) + 1)
	threshold := int32(1 << nbBits)
	gotTotal := int32(0)
	nbBits++

	for remaining > 1 {
		if previous0 {
			n0 := charnum
			for (bitStream & 0xFFFF) == 0xFFFF {
				n0 += 24
				if b.off < iend-5 {
					b.advance(2)
					bitStream = b.Uint32() >> bitCount
				} else {
					bitStream >>= 16
					bitCount += 16
				}
			}
			for (bitStream & 3) == 3 {
				n0 += 3
				bitStream >>= 2
				bitCount += 2
			}
			n0 += uint16(bitStream & 3)
			bitCount += 2
			if n0 > maxSymbolValue {
				return errors.New("maxSymbolValue too small")
			}
			for charnum < n0 {
				s.norm[charnum&0xff] = 0
				charnum++
			}

			if b.off <= iend-7 || b.off+int(bitCount>>3) <= iend-4 {
				b.advance(bitCount >> 3)
				bitCount &= 7
				bitStream = b.Uint32() >> bitCount
			} else {
				bitStream >>= 2
			}
		}

		max := (2*(threshold) - 1) - (remaining)
		var count int32

		if (int32(bitStream) & (threshold - 1)) < max {
			count = int32(bitStream) & (threshold - 1)
			bitCount += nbBits - 1
		} else {
			count = int32(bitStream) & (2*threshold - 1)
			if count >= threshold {
				count -= max
			}
			bitCount += nbBits
		}

		count-- // extra accuracy
		if count < 0 {
			// -1 means +1
			remaining += count
			gotTotal -= count
		} else {
			remaining -= count
			gotTotal += count
		}
		if count > math.MaxInt16 {
			// Only one symbol filling a tableLog 15 table, which cannot be decoded.
			// Truncated, it would leave the decoding table unwritten.
			return fmt.Errorf("corruption detected (count %d > %d)", count, math.MaxInt16)
		}
		s.norm[charnum&0xff] = int16(count)
		charnum++
		previous0 = count == 0
		for remaining < threshold {
			nbBits--
			threshold >>= 1
		}
		if b.off <= iend-7 || b.off+int(bitCount>>3) <= iend-4 {
			b.advance(bitCount >> 3)
			bitCount &= 7
		} else {
			bitCount -= (uint)(8 * (len(b.b) - 4 - b.off))
			b.off = len(b.b) - 4
		}
		bitStream = b.Uint32() >> (bitCount & 31)
	}
	s.symbolLen = charnum

	if s.symbolLen <= 1 {
		return fmt.Errorf("symbolLen (%d) too small", s.symbolLen)
	}
	if s.symbolLen > maxSymbolValue+1 {
		return fmt.Errorf("symbolLen (%d) too big", s.symbolLen)
	}
	if remaining != 1 {
		return fmt.Errorf("corruption detected (remaining %d != 1)", remaining)
	}
	if bitCount > 32 {
		return fmt.Errorf("corruption detected (bitCount %d > 32)", bitCount)
	}
	if gotTotal != 1<<s.actualTableLog {
		return fmt.Errorf("corruption detected (total %d != %d)", gotTotal, 1<<s.actualTableLog)
	}
	b.advance((bitCount + 7) >> 3)
	return nil
}

// decSymbol contains information about a state entry,
// Including the state offset base, the output symbol and
// the number of bits to read for the low part of the destination state.
type decSymbol struct {
	newState uint16
	symbol   uint8
	nbBits   uint8
}

// allocDtable will allocate decoding tables if they are not big enough.
func (s *Scratch) allocDtable() {
	tableSize := 1 << s.actualTableLog
	allocSize := max(tableSize, 256)
	if cap(s.decTable) < allocSize {
		s.decTable = make([]decSymbol, allocSize)
	}
	s.decTable = s.decTable[:tableSize]

	if cap(s.ct.tableSymbol) < 256 {
		s.ct.tableSymbol = make([]byte, 256)
	}
	s.ct.tableSymbol = s.ct.tableSymbol[:256]

	if cap(s.ct.stateTable) < 256 {
		s.ct.stateTable = make([]uint16, 256)
	}
	s.ct.stateTable = s.ct.stateTable[:256]
}

// buildDtable will build the decoding table.
func (s *Scratch) buildDtable() error {
	tableSize := uint32(1 << s.actualTableLog)
	highThreshold := tableSize - 1
	s.allocDtable()
	dt := s.decTable[:tableSize]
	norm := s.norm[:s.symbolLen]
	symbolNext := s.ct.stateTable[:256]

	// Init, lay down lowprob symbols
	s.zeroBits = false
	{
		largeLimit := int16(1 << (s.actualTableLog - 1))
		for i, v := range norm {
			if v == -1 {
				dt[highThreshold].symbol = uint8(i)
				highThreshold--
				symbolNext[i] = 1
			} else {
				if v >= largeLimit {
					s.zeroBits = true
				}
				symbolNext[i] = uint16(v)
			}
		}
	}
	// Spread symbols
	{
		tableMask := tableSize - 1
		step := tableStep(tableSize)
		position := uint32(0)
		if highThreshold == tableMask {
			for ss, v := range norm {
				if v <= 0 {
					continue
				}
				sym := uint8(ss)
				for range int(v) {
					dt[position].symbol = sym
					position = (position + step) & tableMask
				}
			}
		} else {
			for ss, v := range norm {
				if v <= 0 {
					continue
				}
				sym := uint8(ss)
				for range int(v) {
					dt[position].symbol = sym
					position = (position + step) & tableMask
					for position > highThreshold {
						// lowprob area
						position = (position + step) & tableMask
					}
				}
			}
		}
		if position != 0 {
			// position must reach all cells once, otherwise normalizedCounter is incorrect
			return errors.New("corrupted input (position != 0)")
		}
	}

	// Build Decoding table
	{
		tableSize := uint16(1 << s.actualTableLog)
		tl := s.actualTableLog
		for u := range dt {
			symbol := dt[u].symbol
			nextState := symbolNext[symbol]
			symbolNext[symbol] = nextState + 1
			nBits := tl - byte(highBits(uint32(nextState)))
			newState := (nextState << nBits) - tableSize
			if newState >= tableSize {
				return fmt.Errorf("newState (%d) outside table size (%d)", newState, tableSize)
			}
			if newState == uint16(u) && nBits == 0 {
				// Seems weird that this is possible with nbits > 0.
				return fmt.Errorf("newState (%d) == oldState (%d) and no bits", newState, u)
			}
			dt[u] = decSymbol{
				newState: newState,
				symbol:   symbol,
				nbBits:   nBits,
			}
		}
	}
	return nil
}

// decompress will decompress the bitstream.
// If the buffer is over-read an error is returned.
func (s *Scratch) decompress() error {
	br := &s.bits
	if err := br.init(s.br.unread()); err != nil {
		return err
	}

	tl := s.actualTableLog
	dt := s.decTable[:1<<tl]
	st1 := br.getBits(tl)
	st2 := br.getBits(tl)

	in := br.in
	brOff := br.off
	brVal := br.value
	bitsRead := br.bitsRead
	limit := s.DecompressLimit

	// Fast path when output buffer already has capacity (e.g. huff0 weights)
	// and tableLog <= 8 (4 symbols consume <= 32 bits, needing 1 refill per 4).
	if tl <= 8 && cap(s.Out) >= limit {
		dt256 := s.decTable[:256]
		out := s.Out[:cap(s.Out)]
		pos := len(s.Out)
		for brOff >= 4 {
			if bitsRead >= 32 {
				brOff -= 4
				brVal = (brVal << 32) | uint64(binary.LittleEndian.Uint32(in[brOff:]))
				bitsRead -= 32
			}
			if pos+4 > limit {
				// Leave remaining bytes to the exact tail loop below.
				break
			}
			n0 := dt256[uint8(st1)]
			st1 = n0.newState + uint16(((brVal<<(bitsRead&63))>>1)>>((63-n0.nbBits)&63))
			bitsRead += n0.nbBits

			n1 := dt256[uint8(st2)]
			st2 = n1.newState + uint16(((brVal<<(bitsRead&63))>>1)>>((63-n1.nbBits)&63))
			bitsRead += n1.nbBits

			n2 := dt256[uint8(st1)]
			st1 = n2.newState + uint16(((brVal<<(bitsRead&63))>>1)>>((63-n2.nbBits)&63))
			bitsRead += n2.nbBits

			n3 := dt256[uint8(st2)]
			st2 = n3.newState + uint16(((brVal<<(bitsRead&63))>>1)>>((63-n3.nbBits)&63))
			bitsRead += n3.nbBits

			binary.LittleEndian.PutUint32(out[pos:], uint32(n0.symbol)|uint32(n1.symbol)<<8|uint32(n2.symbol)<<16|uint32(n3.symbol)<<24)
			pos += 4
		}
		// Tail loop writing directly into out without append overhead.
		for {
			if bitsRead >= 64 && brOff == 0 && dt[st1].nbBits > 0 {
				if pos+2 <= len(out) {
					out[pos] = dt[st1].symbol
					out[pos+1] = dt[st2].symbol
					s.Out = out[:pos+2]
				} else {
					s.Out = append(out[:pos], dt[st1].symbol, dt[st2].symbol)
				}
				br.off = brOff
				br.value = brVal
				br.bitsRead = bitsRead
				return br.close()
			}
			if bitsRead >= 32 {
				if brOff > 4 {
					brOff -= 4
					brVal = (brVal << 32) | uint64(binary.LittleEndian.Uint32(in[brOff:]))
					bitsRead -= 32
				} else {
					for brOff > 0 {
						brOff--
						brVal = (brVal << 8) | uint64(in[brOff])
						bitsRead -= 8
					}
				}
			}
			n := dt[st1]
			nb := n.nbBits
			var low uint16
			if nb > 0 && bitsRead < 64 {
				low = uint16((brVal << (bitsRead & 63)) >> ((64 - nb) & 63))
				bitsRead += nb
			}
			st1 = n.newState + low
			if pos < len(out) {
				out[pos] = n.symbol
				pos++
			} else {
				out = append(out[:pos], n.symbol)
				out = out[:cap(out)]
				pos++
			}

			if bitsRead >= 64 && brOff == 0 && dt[st2].nbBits > 0 {
				if pos+2 <= len(out) {
					out[pos] = dt[st2].symbol
					out[pos+1] = dt[st1].symbol
					s.Out = out[:pos+2]
				} else {
					s.Out = append(out[:pos], dt[st2].symbol, dt[st1].symbol)
				}
				br.off = brOff
				br.value = brVal
				br.bitsRead = bitsRead
				return br.close()
			}
			n = dt[st2]
			nb = n.nbBits
			low = 0
			if nb > 0 && bitsRead < 64 {
				low = uint16((brVal << (bitsRead & 63)) >> ((64 - nb) & 63))
				bitsRead += nb
			}
			st2 = n.newState + low
			if pos < len(out) {
				out[pos] = n.symbol
				pos++
			} else {
				out = append(out[:pos], n.symbol)
				out = out[:cap(out)]
				pos++
			}
			if pos >= limit {
				s.Out = out[:pos]
				return fmt.Errorf("output size (%d) > DecompressLimit (%d)", pos, limit)
			}
		}
	}

	br.off = brOff
	br.value = brVal
	br.bitsRead = bitsRead
	s1 := decoder{state: st1, br: br, dt: dt}
	s2 := decoder{state: st2, br: br, dt: dt}

	// Use temp table to avoid bound checks/append penalty.
	var tmp = s.ct.tableSymbol[:256]
	var off uint8

	// Main part
	if !s.zeroBits {
		for br.off >= 8 {
			br.fillFast()
			tmp[off+0] = s1.nextFast()
			tmp[off+1] = s2.nextFast()
			br.fillFast()
			tmp[off+2] = s1.nextFast()
			tmp[off+3] = s2.nextFast()
			off += 4
			// When off is 0, we have overflowed and should write.
			if off == 0 {
				s.Out = append(s.Out, tmp...)
				if len(s.Out) >= s.DecompressLimit {
					return fmt.Errorf("output size (%d) > DecompressLimit (%d)", len(s.Out), s.DecompressLimit)
				}
			}
		}
	} else {
		for br.off >= 8 {
			br.fillFast()
			tmp[off+0] = s1.next()
			tmp[off+1] = s2.next()
			br.fillFast()
			tmp[off+2] = s1.next()
			tmp[off+3] = s2.next()
			off += 4
			if off == 0 {
				s.Out = append(s.Out, tmp...)
				// When off is 0, we have overflowed and should write.
				if len(s.Out) >= s.DecompressLimit {
					return fmt.Errorf("output size (%d) > DecompressLimit (%d)", len(s.Out), s.DecompressLimit)
				}
			}
		}
	}
	s.Out = append(s.Out, tmp[:off]...)

	// Final bits, a bit more expensive check
	for {
		if s1.finished() {
			s.Out = append(s.Out, s1.final(), s2.final())
			break
		}
		br.fill()
		s.Out = append(s.Out, s1.next())
		if s2.finished() {
			s.Out = append(s.Out, s2.final(), s1.final())
			break
		}
		s.Out = append(s.Out, s2.next())
		if len(s.Out) >= s.DecompressLimit {
			return fmt.Errorf("output size (%d) > DecompressLimit (%d)", len(s.Out), s.DecompressLimit)
		}
	}
	return br.close()
}

// decoder keeps track of the current state and updates it from the bitstream.
type decoder struct {
	state uint16
	br    *bitReader
	dt    []decSymbol
}

// init will initialize the decoder and read the first state from the stream.
func (d *decoder) init(in *bitReader, dt []decSymbol, tableLog uint8) {
	d.dt = dt
	d.br = in
	d.state = in.getBits(tableLog)
}

// next returns the next symbol and sets the next state.
// At least tablelog bits must be available in the bit reader.
func (d *decoder) next() uint8 {
	n := &d.dt[d.state]
	lowBits := d.br.getBits(n.nbBits)
	d.state = n.newState + lowBits
	return n.symbol
}

// finished returns true if all bits have been read from the bitstream
// and the next state would require reading bits from the input.
func (d *decoder) finished() bool {
	return d.br.finished() && d.dt[d.state].nbBits > 0
}

// final returns the current state symbol without decoding the next.
func (d *decoder) final() uint8 {
	return d.dt[d.state].symbol
}

// nextFast returns the next symbol and sets the next state.
// This can only be used if no symbols are 0 bits.
// At least tablelog bits must be available in the bit reader.
func (d *decoder) nextFast() uint8 {
	n := d.dt[d.state]
	lowBits := d.br.getBitsFast(n.nbBits)
	d.state = n.newState + lowBits
	return n.symbol
}
