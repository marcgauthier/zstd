//go:build (!amd64 && !arm64) || noasm || !gc

package zstd

const useMatchAsm = false

func doubleFastEncodeAsm(e *doubleFastEncoder, blk *blockEnc, src []byte, s, sLimit int32, saveOffsets bool) {
}

func fastEncodeAsm(e *fastEncoder, blk *blockEnc, src []byte, s, sLimit int32, saveOffsets bool) {
}

func betterFastEncodeAsm(e *betterFastEncoder, blk *blockEnc, src []byte, s, sLimit int32, saveOffsets bool) {
}

func bestFastEncodeAsm(e *bestFastEncoder, blk *blockEnc, src []byte, s, sLimit, bitsPerByte int32, saveOffsets bool) {
}
