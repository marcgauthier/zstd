//go:build (amd64 || arm64) && !nounsafe

package huff0

import "unsafe"

// fillSingle fills dst (power-of-two length) with d using 64-bit stores.
func fillSingle(dst []dEntrySingle, d dEntrySingle) {
	n := len(dst)
	if n >= 4 {
		v := uint64(d.entry) * 0x0001000100010001
		p := unsafe.Pointer(&dst[0])
		if n >= 8 {
			for i := 0; i < n; i += 8 {
				*(*uint64)(unsafe.Add(p, uintptr(i)*2)) = v
				*(*uint64)(unsafe.Add(p, uintptr(i+4)*2)) = v
			}
		} else {
			*(*uint64)(p) = v
		}
		return
	}
	if n == 2 {
		v := uint32(d.entry) | uint32(d.entry)<<16
		*(*uint32)(unsafe.Pointer(&dst[0])) = v
		return
	}
	if n == 1 {
		dst[0] = d
	}
}

// fillDouble fills dst with d using 64-bit stores.
func fillDouble(dst []dEntryDouble, d dEntryDouble) {
	n := len(dst)
	if n >= 2 {
		v := uint64(d) | uint64(d)<<32
		p := unsafe.Pointer(&dst[0])
		i := 0
		for ; i+4 <= n; i += 4 {
			*(*uint64)(unsafe.Add(p, uintptr(i)*4)) = v
			*(*uint64)(unsafe.Add(p, uintptr(i+2)*4)) = v
		}
		for ; i+2 <= n; i += 2 {
			*(*uint64)(unsafe.Add(p, uintptr(i)*4)) = v
		}
		if i < n {
			dst[i] = d
		}
		return
	}
	if n == 1 {
		dst[0] = d
	}
}
