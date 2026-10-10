package schedule

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// SystemMemory 返回物理内存字节数，读不到时返回 0。
func SystemMemory() uint64 {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kib, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0
			}
			return kib << 10
		}
	}
	return 0
}
