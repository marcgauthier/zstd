// Copied from S2 implementation.

//go:build !noasm && gc

#include "textflag.h"

// func matchLen(a []byte, b []byte) int
TEXT ·matchLen(SB), NOSPLIT, $0-56
	MOVQ a_base+0(FP), AX
	MOVQ b_base+24(FP), CX
	MOVQ a_len+8(FP), DX

	XORQ SI, SI
	CMPQ DX, $16
	JB   matchlen_check8

matchlen_loop16:
	MOVOU    (AX)(SI*1), X0
	MOVOU    (CX)(SI*1), X1
	PCMPEQB  X1, X0
	PMOVMSKB X0, BX
	XORL     $0xffff, BX
	JNZ      matchlen_found16
	SUBQ     $16, DX
	ADDQ     $16, SI
	CMPQ     DX, $16
	JAE      matchlen_loop16

matchlen_check8:
	CMPQ DX, $0x08
	JB   matchlen_match4_standalone

matchlen_loopback_standalone:
	MOVQ (AX)(SI*1), BX
	XORQ (CX)(SI*1), BX
	JZ   matchlen_loop_standalone

#ifdef GOAMD64_v3
	TZCNTQ BX, BX
#else
	BSFQ BX, BX
#endif
	SHRL $0x03, BX
	ADDQ BX, SI
	JMP  gen_match_len_end

matchlen_found16:
#ifdef GOAMD64_v3
	TZCNTL BX, BX
#else
	BSFL BX, BX
#endif
	ADDQ BX, SI
	JMP  gen_match_len_end

matchlen_loop_standalone:
	SUBQ $8, DX
	ADDQ $8, SI
	CMPQ DX, $0x08
	JAE  matchlen_loopback_standalone

matchlen_match4_standalone:
	CMPQ DX, $0x04
	JB   matchlen_match2_standalone
	MOVL (AX)(SI*1), BX
	CMPL (CX)(SI*1), BX
	JNE  matchlen_match2_standalone
	SUBQ $4, DX
	ADDQ $4, SI

matchlen_match2_standalone:
	CMPQ DX, $0x02
	JB   matchlen_match1_standalone
	MOVW (AX)(SI*1), BX
	CMPW (CX)(SI*1), BX
	JNE  matchlen_match1_standalone
	SUBQ $2, DX
	ADDQ $2, SI

matchlen_match1_standalone:
	CMPQ DX, $0x01
	JB   gen_match_len_end
	MOVB (AX)(SI*1), BL
	CMPB (CX)(SI*1), BL
	JNE  gen_match_len_end
	INCQ SI

gen_match_len_end:
	MOVQ SI, ret+48(FP)
	RET
