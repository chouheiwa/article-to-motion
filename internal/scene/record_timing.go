package scene

// timingInputFiles 列出时间合同引用的镜头内文件，供输入指纹使用。
//
// 镜头局部数据 am-scene.js 不算：它由 scene.json、字幕、视觉规范与这里的
// 短语时间轴推出，每次渲染前重写，算进去只会重复计数。
func timingInputFiles(s Scene) []string {
	if s.Timing == nil || s.Timing.Phrases == "" {
		return nil
	}
	return []string{s.Timing.Phrases}
}
