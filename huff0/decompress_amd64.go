//go:build amd64 && !noasm && gc

// amd64 stubs and dispatch for the asm loops used by decompress_asm.go.
package huff0

import (
	"github.com/klauspost/compress/internal/cpuinfo"
)

// decompress4x_main_loop_amd64 is an x86 assembler implementation
// of Decompress4X when tablelog > 8, decoding fastSymbols symbols per
// stream between bit container reloads.
//
//go:noescape
func decompress4x_main_loop_amd64(ctx *decompress4xContext)

// decompress4x_8b_main_loop_amd64 is an x86 assembler implementation
// of Decompress4X when tablelog <= 8, decoding fast8bSymbols symbols
// per stream between bit container reloads.
//
//go:noescape
func decompress4x_8b_main_loop_amd64(ctx *decompress4xContext)

// decompress4x_4b_main_loop_amd64 is an x86 assembler implementation
// of Decompress4X when tablelog <= 4, decoding fast4bSymbols symbols
// per stream between bit container reloads.
//
//go:noescape
func decompress4x_4b_main_loop_amd64(ctx *decompress4xContext)

// decompress4x_4b_main_loop_bmi2 is the BMI2 twin of decompress4x_4b_main_loop_amd64.
//
//go:noescape
func decompress4x_4b_main_loop_bmi2(ctx *decompress4xContext)

// decompress4x_main_loop_bmi2 is the BMI2 twin of decompress4x_main_loop_amd64.
//
//go:noescape
func decompress4x_main_loop_bmi2(ctx *decompress4xContext)

// decompress4x_8b_main_loop_bmi2 is the BMI2 twin of decompress4x_8b_main_loop_amd64.
//
//go:noescape
func decompress4x_8b_main_loop_bmi2(ctx *decompress4xContext)

// decompress1x_main_loop_amd64 is an x86 assembler implementation
// of Decompress1X when tablelog > 8, decoding fastSymbols symbols
// between bit container reloads.
//
//go:noescape
func decompress1x_main_loop_amd64(ctx *decompress1xContext)

// decompress1x_8b_main_loop_amd64 is an x86 assembler implementation
// of Decompress1X when tablelog <= 8, decoding fast8bSymbols symbols
// between bit container reloads.
//
//go:noescape
func decompress1x_8b_main_loop_amd64(ctx *decompress1xContext)

// decompress1x_4b_main_loop_amd64 is an x86 assembler implementation
// of Decompress1X when tablelog <= 4, decoding fast4bSymbols symbols
// between bit container reloads.
//
//go:noescape
func decompress1x_4b_main_loop_amd64(ctx *decompress1xContext)

// decompress1x_main_loop_bmi2 is the BMI2 twin of decompress1x_main_loop_amd64.
//
//go:noescape
func decompress1x_main_loop_bmi2(ctx *decompress1xContext)

// decompress1x_8b_main_loop_bmi2 is the BMI2 twin of decompress1x_8b_main_loop_amd64.
//
//go:noescape
func decompress1x_8b_main_loop_bmi2(ctx *decompress1xContext)

// decompress1x_4b_main_loop_bmi2 is the BMI2 twin of decompress1x_4b_main_loop_amd64.
//
//go:noescape
func decompress1x_4b_main_loop_bmi2(ctx *decompress1xContext)

func decompress4x_main_loop_asm(ctx *decompress4xContext) {
	if cpuinfo.HasBMI2() {
		decompress4x_main_loop_bmi2(ctx)
	} else {
		decompress4x_main_loop_amd64(ctx)
	}
}

func decompress4x_8b_main_loop_asm(ctx *decompress4xContext) {
	if cpuinfo.HasBMI2() {
		decompress4x_8b_main_loop_bmi2(ctx)
	} else {
		decompress4x_8b_main_loop_amd64(ctx)
	}
}

func decompress4x_4b_main_loop_asm(ctx *decompress4xContext) {
	if cpuinfo.HasBMI2() {
		decompress4x_4b_main_loop_bmi2(ctx)
	} else {
		decompress4x_4b_main_loop_amd64(ctx)
	}
}

func decompress1x_main_loop_asm(ctx *decompress1xContext) {
	if cpuinfo.HasBMI2() {
		decompress1x_main_loop_bmi2(ctx)
	} else {
		decompress1x_main_loop_amd64(ctx)
	}
}

func decompress1x_8b_main_loop_asm(ctx *decompress1xContext) {
	if cpuinfo.HasBMI2() {
		decompress1x_8b_main_loop_bmi2(ctx)
	} else {
		decompress1x_8b_main_loop_amd64(ctx)
	}
}

func decompress1x_4b_main_loop_asm(ctx *decompress1xContext) {
	if cpuinfo.HasBMI2() {
		decompress1x_4b_main_loop_bmi2(ctx)
	} else {
		decompress1x_4b_main_loop_amd64(ctx)
	}
}

//go:noescape
func decompress4x2_main_loop_amd64(ctx *decompress4x2Context)

//go:noescape
func decompress4x2_8b_main_loop_amd64(ctx *decompress4x2Context)

//go:noescape
func decompress4x2_4b_main_loop_amd64(ctx *decompress4x2Context)

//go:noescape
func decompress4x2_main_loop_bmi2(ctx *decompress4x2Context)

//go:noescape
func decompress4x2_8b_main_loop_bmi2(ctx *decompress4x2Context)

//go:noescape
func decompress4x2_4b_main_loop_bmi2(ctx *decompress4x2Context)

//go:noescape
func decompress1x2_main_loop_amd64(ctx *decompress1x2Context)

//go:noescape
func decompress1x2_8b_main_loop_amd64(ctx *decompress1x2Context)

//go:noescape
func decompress1x2_4b_main_loop_amd64(ctx *decompress1x2Context)

//go:noescape
func decompress1x2_main_loop_bmi2(ctx *decompress1x2Context)

//go:noescape
func decompress1x2_8b_main_loop_bmi2(ctx *decompress1x2Context)

//go:noescape
func decompress1x2_4b_main_loop_bmi2(ctx *decompress1x2Context)

func decompress4x2_main_loop_asm(ctx *decompress4x2Context) {
	if cpuinfo.HasBMI2() {
		decompress4x2_main_loop_bmi2(ctx)
	} else {
		decompress4x2_main_loop_amd64(ctx)
	}
}

func decompress4x2_8b_main_loop_asm(ctx *decompress4x2Context) {
	if cpuinfo.HasBMI2() {
		decompress4x2_8b_main_loop_bmi2(ctx)
	} else {
		decompress4x2_8b_main_loop_amd64(ctx)
	}
}

func decompress4x2_4b_main_loop_asm(ctx *decompress4x2Context) {
	if cpuinfo.HasBMI2() {
		decompress4x2_4b_main_loop_bmi2(ctx)
	} else {
		decompress4x2_4b_main_loop_amd64(ctx)
	}
}

func decompress1x2_main_loop_asm(ctx *decompress1x2Context) {
	if cpuinfo.HasBMI2() {
		decompress1x2_main_loop_bmi2(ctx)
	} else {
		decompress1x2_main_loop_amd64(ctx)
	}
}

func decompress1x2_8b_main_loop_asm(ctx *decompress1x2Context) {
	if cpuinfo.HasBMI2() {
		decompress1x2_8b_main_loop_bmi2(ctx)
	} else {
		decompress1x2_8b_main_loop_amd64(ctx)
	}
}

func decompress1x2_4b_main_loop_asm(ctx *decompress1x2Context) {
	if cpuinfo.HasBMI2() {
		decompress1x2_4b_main_loop_bmi2(ctx)
	} else {
		decompress1x2_4b_main_loop_amd64(ctx)
	}
}
