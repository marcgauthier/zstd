//go:build (amd64 || arm64) && !noasm && gc

// This file contains the specialisation of Decoder.Decompress4X
// and Decoder.Decompress1X that use an asm implementation of their main loops.
// The asm function stubs and any per-arch dispatch live in decompress_amd64.go
// and decompress_arm64.go.
package huff0

import (
	"errors"
	"fmt"
)

// fallback8BitSize is the size where using Go version is faster.
const fallback8BitSize = 800

// decompress4xContext is the argument block of the Decompress4X asm loops.
// Go fills every field but decoded and inner; the asm advances ip, and on
// return leaves each bit reader in the form bitReaderShifted.restoreFromAsm
// expects.
type decompress4xContext struct {
	pbr      *[4]bitReaderShifted
	peekBits uint8
	out      *byte
	dstEvery int
	tbl      *dEntrySingle
	decoded  int
	limit    *byte    // stream 0's output pointer must stay below this
	ilowest  *byte    // start of the input block; no read goes below it
	ip       [4]*byte // each stream's 8-byte input window, advanced by the asm
}

// decompress4x2Context is the argument block of the 2-symbol Decompress4X asm loops.
type decompress4x2Context struct {
	pbr      *[4]bitReaderShifted
	peekBits uint8
	out      *byte
	dstEvery int
	dstSize  int
	tbl      *dEntryDouble
	decoded  [4]int
	ilowest  *byte
	ip       [4]*byte
}

// decompress1x2Context is the argument block of the 2-symbol Decompress1X asm loops.
type decompress1x2Context struct {
	pbr      *bitReaderShifted
	peekBits uint8
	out      *byte
	outCap   int
	tbl      *dEntryDouble
	decoded  int
	ilowest  *byte
	ip       *byte
}

// Symbols decoded per stream between reloads by the 4X asm loops; must
// match the constants of the same names in _generate/gen.go.
const (
	fastSymbols   = 5  // tablelog 9..11
	fast8bSymbols = 7  // tablelog 5..8
	fast4bSymbols = 14 // tablelog <= 4
)

// Decompress4X will decompress a 4X encoded stream.
// The length of the supplied input must match the end of a block exactly.
// The *capacity* of the dst slice must match the destination size of
// the uncompressed data exactly.
func (d *Decoder) Decompress4X(dst, src []byte) ([]byte, error) {
	if len(d.dt.single) == 0 {
		return nil, errors.New("no table loaded")
	}
	if len(src) < 6+(4*1) {
		return nil, errors.New("input too small")
	}

	use8BitTables := d.actualTableLog <= 8
	if cap(dst) < fallback8BitSize && use8BitTables && (!d.dt.doubleValid || cap(dst) < 256) {
		return d.decompress4X8bit(dst, src)
	}

	var br [4]bitReaderShifted
	// Decode "jump table"
	start := 6
	for i := range 3 {
		length := int(src[i*2]) | (int(src[i*2+1]) << 8)
		if start+length >= len(src) {
			return nil, errors.New("truncated input (or invalid offset)")
		}
		err := br[i].init(src[start : start+length])
		if err != nil {
			return nil, err
		}
		start += length
	}
	err := br[3].init(src[start:])
	if err != nil {
		return nil, err
	}

	// destination, offset to match first output
	dstSize := cap(dst)
	dst = dst[:dstSize]
	out := dst
	dstEvery := (dstSize + 3) / 4

	const tlSize = 1 << tableLogMax
	const tlMask = tlSize - 1
	single := d.dt.single[:tlSize]

	var decoded int
	var streamDecoded [4]int

	nSyms := fastSymbols
	if d.actualTableLog <= 4 {
		nSyms = fast4bSymbols
	} else if use8BitTables {
		nSyms = fast8bSymbols
	}
	if d.dt.doubleValid && dstSize >= 32 && dstEvery >= 16 && br[0].canUseAsm() && br[1].canUseAsm() && br[2].canUseAsm() && br[3].canUseAsm() {
		dl := d.dt.doubleLog
		for i := range br {
			br[i].prepareForAsm()
		}
		ctx := decompress4x2Context{
			pbr:      &br,
			peekBits: uint8((64 - dl) & 63),
			out:      &out[0],
			dstEvery: dstEvery,
			dstSize:  dstSize,
			tbl:      &d.dt.double[0],
			ilowest:  &src[0],
		}
		for i := range br {
			ctx.ip[i] = &br[i].in[br[i].off]
		}
		if dl <= 4 {
			decompress4x2_4b_main_loop_asm(&ctx)
		} else if dl <= 8 {
			decompress4x2_8b_main_loop_asm(&ctx)
		} else {
			decompress4x2_main_loop_asm(&ctx)
		}
		streamDecoded = ctx.decoded
		decoded = streamDecoded[0] + streamDecoded[1] + streamDecoded[2] + streamDecoded[3]
		for i := range br {
			if err := br[i].restoreFromAsm(); err != nil {
				return nil, err
			}
		}
	} else if limit := dstEvery - nSyms - 2; limit > 0 && br[0].canUseAsm() && br[1].canUseAsm() && br[2].canUseAsm() && br[3].canUseAsm() {
		for i := range br {
			br[i].prepareForAsm()
		}
		ctx := decompress4xContext{
			pbr:      &br,
			peekBits: uint8((64 - d.actualTableLog) & 63), // see: bitReaderShifted.peekBitsFast()
			out:      &out[0],
			dstEvery: dstEvery,
			tbl:      &single[0],
			limit:    &out[limit],
			// The 6-byte jump table sits below stream 0 inside src, so the
			// lowest window may reach into it; restoreFromAsm accounts for
			// bytes below a stream's start. Bounding by src rather than by
			// stream 0 keeps the asm running about six bytes longer, which
			// matters for streams with very short codes.
			ilowest: &src[0],
		}
		for i := range br {
			ctx.ip[i] = &br[i].in[br[i].off]
		}
		switch nSyms {
		case fast4bSymbols:
			decompress4x_4b_main_loop_asm(&ctx)
		case fast8bSymbols:
			decompress4x_8b_main_loop_asm(&ctx)
		default:
			decompress4x_main_loop_asm(&ctx)
		}

		decoded = ctx.decoded
		perStream := decoded / 4
		streamDecoded = [4]int{perStream, perStream, perStream, perStream}
		for i := range br {
			if err := br[i].restoreFromAsm(); err != nil {
				return nil, err
			}
		}
	}

	// Decode remaining.
	for i := range br {
		startOff := dstEvery*i + streamDecoded[i]
		offset := startOff
		endsAt := min(dstEvery*(i+1), dstSize)
		br := &br[i]
		bitsLeft := br.remaining()
		for bitsLeft > 0 {
			br.fill()
			if offset >= endsAt {
				return nil, errors.New("corruption detected: stream overrun 4")
			}

			// Read value and increment offset.
			val := br.peekBitsFast(d.actualTableLog)
			v := single[val&tlMask].entry
			nBits := uint8(v)
			br.advance(nBits)
			bitsLeft -= uint(nBits)
			out[offset] = uint8(v >> 8)
			offset++
		}
		if offset != endsAt {
			return nil, fmt.Errorf("corruption detected: short output block %d, end %d != %d", i, offset, endsAt)
		}
		decoded += offset - startOff
		err = br.close()
		if err != nil {
			return nil, err
		}
	}
	if dstSize != decoded {
		return nil, errors.New("corruption detected: short output block")
	}
	return dst, nil
}

// decompress1xContext is the argument block of the Decompress1X asm loops.
// Go fills every field but decoded; the asm advances ip, and on return
// leaves the bit reader in the form bitReaderShifted.restoreFromAsm expects.
type decompress1xContext struct {
	pbr      *bitReaderShifted
	peekBits uint8
	out      *byte
	outCap   int // no write reaches out[outCap]
	tbl      *dEntrySingle
	decoded  int
	ilowest  *byte // start of the stream; no read goes below it
	ip       *byte // the 8-byte input window, advanced by the asm
}

// Decompress1X will decompress a 1X encoded stream.
// The cap of the output buffer will be the maximum decompressed size.
// The length of the supplied input must match the end of a block exactly.
func (d *Decoder) Decompress1X(dst, src []byte) ([]byte, error) {
	if len(d.dt.single) == 0 {
		return nil, errors.New("no table loaded")
	}
	var br bitReaderShifted
	err := br.init(src)
	if err != nil {
		return dst, err
	}
	maxDecodedSize := cap(dst)
	dst = dst[:maxDecodedSize]

	const tlSize = 1 << tableLogMax
	const tlMask = tlSize - 1

	// The asm decodes whole batches of symbols and only re-checks its
	// bounds between them, so it needs at least one batch of room (the
	// output bound rounds nSyms up to 16, see _generate/gen.go) and a full
	// 8-byte window ahead of the read pointer to enter the loop. It also
	// needs at most 7 bits consumed on entry so that a batch cannot run
	// the container dry, which prepareForAsm establishes.
	decoded := 0
	if d.dt.doubleValid && maxDecodedSize >= 32 && br.canUseAsm() {
		dl := d.dt.doubleLog
		br.prepareForAsm()
		ctx := decompress1x2Context{
			pbr:      &br,
			out:      &dst[0],
			outCap:   maxDecodedSize,
			peekBits: uint8((64 - dl) & 63),
			tbl:      &d.dt.double[0],
			ilowest:  &src[0],
			ip:       &br.in[br.off],
		}
		if dl <= 4 {
			decompress1x2_4b_main_loop_asm(&ctx)
		} else if dl <= 8 {
			decompress1x2_8b_main_loop_asm(&ctx)
		} else {
			decompress1x2_main_loop_asm(&ctx)
		}
		decoded = ctx.decoded
	} else if maxDecodedSize >= 16 && br.canUseAsm() {
		br.prepareForAsm()
		ctx := decompress1xContext{
			pbr:      &br,
			out:      &dst[0],
			outCap:   maxDecodedSize,
			peekBits: uint8((64 - d.actualTableLog) & 63), // see: bitReaderShifted.peekBitsFast()
			tbl:      &d.dt.single[0],
			ilowest:  &src[0],
			ip:       &br.in[br.off],
		}
		if d.actualTableLog <= 4 {
			decompress1x_4b_main_loop_asm(&ctx)
		} else if d.actualTableLog <= 8 {
			decompress1x_8b_main_loop_asm(&ctx)
		} else {
			decompress1x_main_loop_asm(&ctx)
		}
		decoded = ctx.decoded
	}
	dst = dst[:decoded]

	bitsLeft := br.remaining()
	for bitsLeft > 0 {
		br.fill()
		if len(dst) >= maxDecodedSize {
			br.close()
			return nil, ErrMaxDecodedSizeExceeded
		}
		v := d.dt.single[br.peekBitsFast(d.actualTableLog)&tlMask]
		nBits := uint8(v.entry)
		br.advance(nBits)
		bitsLeft -= uint(nBits)
		dst = append(dst, uint8(v.entry>>8))
	}
	return dst, br.close()
}
