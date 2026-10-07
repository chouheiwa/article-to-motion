//go:build !darwin && !linux

package schedule

// SystemMemory 在没有实现的平台上返回 0，渲染并发只按 CPU 分。
func SystemMemory() uint64 { return 0 }
