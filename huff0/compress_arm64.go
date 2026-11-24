//go:build arm64 && !noasm && gc

package huff0

//go:noescape
func compress1xDo_arm64(ctx *compress1xDoCtx)

func compress1xDoAsm(ctx *compress1xDoCtx) {
	compress1xDo_arm64(ctx)
}
