//go:build amd64 && !noasm && gc

package zstd

import "github.com/klauspost/compress/internal/cpuinfo"

//go:noescape
func encodeSeqs_amd64(ctx *encodeSeqsCtx)

//go:noescape
func encodeSeqs_bmi2(ctx *encodeSeqsCtx)

func encodeSeqsAsm(ctx *encodeSeqsCtx) {
	if cpuinfo.HasBMI2() {
		encodeSeqs_bmi2(ctx)
		return
	}
	encodeSeqs_amd64(ctx)
}
