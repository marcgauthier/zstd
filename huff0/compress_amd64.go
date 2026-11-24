//go:build amd64 && !noasm && gc

package huff0

import "github.com/klauspost/compress/internal/cpuinfo"

//go:noescape
func compress1xDo_amd64(ctx *compress1xDoCtx)

//go:noescape
func compress1xDo_bmi2(ctx *compress1xDoCtx)

func compress1xDoAsm(ctx *compress1xDoCtx) {
	if cpuinfo.HasBMI2() {
		compress1xDo_bmi2(ctx)
		return
	}
	compress1xDo_amd64(ctx)
}
