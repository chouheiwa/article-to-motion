package schedule

import "golang.org/x/sys/unix"

// SystemMemory 返回物理内存字节数，读不到时返回 0。
func SystemMemory() uint64 {
	value, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return value
}
