//go:build arm64 && !noasm && gc

package zstd

//go:noescape
func encodeSeqs_arm64(ctx *encodeSeqsCtx)

func encodeSeqsAsm(ctx *encodeSeqsCtx) {
	encodeSeqs_arm64(ctx)
}
