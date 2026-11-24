//go:build (!amd64 && !arm64) || nounsafe

package huff0

// fillSingle fills dst with d.
func fillSingle(dst []dEntrySingle, d dEntrySingle) {
	if len(dst) >= 4 {
		for i := 0; i < len(dst); i += 4 {
			s4 := dst[i : i+4 : i+4]
			s4[0], s4[1], s4[2], s4[3] = d, d, d, d
		}
		return
	}
	for i := range dst {
		dst[i] = d
	}
}

// fillDouble fills dst with d.
func fillDouble(dst []dEntryDouble, d dEntryDouble) {
	for i := range dst {
		dst[i] = d
	}
}
