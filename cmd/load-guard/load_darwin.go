package main

import (
	"encoding/binary"
	"syscall"
)

// invariant: vm.loadavg is struct loadavg { uint32 ldavg[3]; int64 fscale } at offsets 0 and 16;
// syscall.Sysctl drops one trailing NUL byte, so the buffer is zero-padded back to 24 bytes.
func fiveMinuteLoad() (float64, bool) {
	raw, err := syscall.Sysctl("vm.loadavg")
	if err != nil {
		return 0, false
	}
	buf := make([]byte, 24)
	copy(buf, raw)
	scale := binary.LittleEndian.Uint64(buf[16:24])
	if scale == 0 {
		return 0, false
	}
	return float64(binary.LittleEndian.Uint32(buf[4:8])) / float64(scale), true
}
