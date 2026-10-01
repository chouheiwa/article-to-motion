// Package mediaprobe 把 ffprobe / ffmpeg 的实测结果解析成结构体，并按期望规格断言。
//
// 存在的理由：PROMPT-PRODUCTION.md 的第八、十一阶段列出了一整张成片机器检查表
// （画幅、30fps、H.264、yuv420p、帧数、音轨、采样率、可完整解码），但这些检查过去
// 只写在提示词里——每次运行都由 AI CLI 现场自己拼 ffprobe 参数，拼法每次不同、
// 漏检不报错。把这张表变成代码，检查项才有单一实现和单一真相源。
//
// 设计上的两条硬约束：
//
//   - Spec 的零值字段表示「不检查」，调用方只想查一件事时不必把全部字段填满。
//   - Check 返回全部不符项而不是第一条。验收门每次只报一条，调用方就得
//     「修一条、重跑一次、再发现下一条」，一次成片检查要循环好几轮。
package mediaprobe

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/envutil"
	"github.com/chouheiwa/articale-to-motion/internal/fsutil"
)

// VideoStream 是视频流的实测规格。
type VideoStream struct {
	Codec       string `json:"codec"`
	WidthPx     int    `json:"width_px"`
	HeightPx    int    `json:"height_px"`
	PixelFormat string `json:"pixel_format"`
	FPSNum      int    `json:"fps_num"`
	FPSDen      int    `json:"fps_den"`
	// NBFrames 是容器声明的帧数。0 表示容器没声明，不代表 0 帧——
	// 精确帧数要用 CountFrames 解码统计。
	NBFrames       int    `json:"nb_frames"`
	ColorSpace     string `json:"color_space,omitempty"`
	ColorPrimaries string `json:"color_primaries,omitempty"`
	ColorTransfer  string `json:"color_transfer,omitempty"`
	// DurationSeconds 是该流自己声明的时长，0 表示没声明。
	// 与容器时长分开记：成片的音画是否等长只能按流比。
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
}

// FPS 返回帧率。ffprobe 用分数表示帧率，29.97 写成 30000/1001。
func (v VideoStream) FPS() float64 {
	if v.FPSDen == 0 {
		return 0
	}
	return float64(v.FPSNum) / float64(v.FPSDen)
}

// Resolution 返回 "宽x高" 形式，供错误信息直接引用。
func (v VideoStream) Resolution() string {
	return fmt.Sprintf("%dx%d", v.WidthPx, v.HeightPx)
}

// AudioStream 是音频流的实测规格。
type AudioStream struct {
	Codec      string `json:"codec"`
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
	// DurationSeconds 是该流自己声明的时长，0 表示没声明。
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
}

// Media 是一个媒体文件的实测规格。没有音轨时 Audio 为 nil。
type Media struct {
	Path            string       `json:"path"`
	DurationSeconds float64      `json:"duration_seconds"`
	Video           *VideoStream `json:"video"`
	Audio           *AudioStream `json:"audio"`
}

// Audio 表示对音轨存在与否的期望。
type Audio uint8

const (
	// AudioIgnore 不检查音轨。
	AudioIgnore Audio = iota
	// AudioAbsent 要求没有音轨，用于镜头产物和静音母版。
	AudioAbsent
	// AudioPresent 要求有音轨，用于混音后的成片。
	AudioPresent
)

// Spec 是期望规格。零值字段表示不检查该项。
type Spec struct {
	WidthPx         int
	HeightPx        int
	FPS             int
	Codec           string
	PixelFormat     string
	DurationSeconds float64
	Tolerance       float64
	// Frames 是期望的总帧数。与实测值比较前要先确认实测值已知。
	Frames     int
	Audio      Audio
	SampleRate int
	Channels   int
}

// Validate 检查 Spec 自身是否自洽，在跑任何外部命令之前调用。
func (s Spec) Validate() error {
	if s.DurationSeconds != 0 {
		if math.IsNaN(s.Tolerance) || math.IsInf(s.Tolerance, 0) || s.Tolerance < 0 {
			return fmt.Errorf("时长容差必须是有限且非负的数字")
		}
		if math.IsNaN(s.DurationSeconds) || math.IsInf(s.DurationSeconds, 0) || s.DurationSeconds < 0 {
			return fmt.Errorf("期望时长必须是有限且非负的数字")
		}
	}
	return nil
}

// Check 逐项比对并返回全部不符项。相符时返回空切片。
//
// 每条不符项都带实测值：只说「分辨率不符」的话，调用方还得自己再跑一次
// ffprobe 才知道差在哪，等于把这个包的价值抵消掉一半。
func (s Spec) Check(m Media) []string {
	var problems []string
	v := m.Video
	if v == nil {
		return append(problems, "文件没有视频流")
	}
	if s.WidthPx > 0 && s.HeightPx > 0 && (v.WidthPx != s.WidthPx || v.HeightPx != s.HeightPx) {
		problems = append(problems, fmt.Sprintf("分辨率不符：期望 %dx%d，实测 %s", s.WidthPx, s.HeightPx, v.Resolution()))
	}
	if s.FPS > 0 && math.Abs(v.FPS()-float64(s.FPS)) > fpsTolerance {
		problems = append(problems, fmt.Sprintf("帧率不符：期望 %d，实测 %s", s.FPS, formatFPS(v.FPS())))
	}
	if s.Codec != "" && !strings.EqualFold(v.Codec, s.Codec) {
		problems = append(problems, fmt.Sprintf("视频编码不符：期望 %s，实测 %s", s.Codec, orUnknown(v.Codec)))
	}
	if s.PixelFormat != "" && !strings.EqualFold(v.PixelFormat, s.PixelFormat) {
		problems = append(problems, fmt.Sprintf("像素格式不符：期望 %s，实测 %s", s.PixelFormat, orUnknown(v.PixelFormat)))
	}
	if s.DurationSeconds > 0 && math.Abs(m.DurationSeconds-s.DurationSeconds) > s.Tolerance {
		problems = append(problems, fmt.Sprintf("时长不符：期望 %.3f 秒（容差 %.3f），实测 %.3f 秒",
			s.DurationSeconds, s.Tolerance, m.DurationSeconds))
	}
	// NBFrames == 0 是「容器没声明」，不是「0 帧」。把未知当成不符会让
	// 所有不声明 nb_frames 的容器一律失败。
	if s.Frames > 0 && v.NBFrames > 0 && v.NBFrames != s.Frames {
		problems = append(problems, fmt.Sprintf("总帧数不符：期望 %d，实测 %d", s.Frames, v.NBFrames))
	}
	switch s.Audio {
	case AudioAbsent:
		if m.Audio != nil {
			problems = append(problems, fmt.Sprintf("应当没有音轨，实测存在 %s 音轨", orUnknown(m.Audio.Codec)))
		}
	case AudioPresent:
		if m.Audio == nil {
			problems = append(problems, "应当有音轨，实测没有音轨")
		}
	}
	if m.Audio != nil {
		if s.SampleRate > 0 && m.Audio.SampleRate != s.SampleRate {
			problems = append(problems, fmt.Sprintf("音频采样率不符：期望 %d，实测 %d", s.SampleRate, m.Audio.SampleRate))
		}
		if s.Channels > 0 && m.Audio.Channels != s.Channels {
			problems = append(problems, fmt.Sprintf("音频声道数不符：期望 %d，实测 %d", s.Channels, m.Audio.Channels))
		}
	}
	return problems
}

// fpsTolerance 容纳 29.97 这类分数帧率与整数声明之间的差。
// 取 0.05：29.97 与 30 相差 0.03 会被判定为「不是 30fps」，
// 而 25 与 30 相差 5 显然超出——两者之间没有需要区分的真实帧率。
const fpsTolerance = 0.02

func formatFPS(value float64) string {
	if value == math.Trunc(value) {
		return strconv.Itoa(int(value))
	}
	return strconv.FormatFloat(value, 'f', 3, 64)
}

func orUnknown(value string) string {
	if value == "" {
		return "未知"
	}
	return value
}

// Report 是一次校验的机器可读结果。
//
// 与 schedule.Report 同样的取向：调用方应当读这份 JSON 判断结果，
// 而不是解析给人看的中文输出。字段一旦发布就不再删改，只做向后兼容的追加。
type Report struct {
	SchemaVersion int    `json:"schema_version"`
	Path          string `json:"path"`
	// OK 只由 Problems 决定。Hints 不影响判定。
	OK       bool     `json:"ok"`
	Media    Media    `json:"media"`
	Problems []string `json:"problems"`
	// Hints 是值得人看一眼、但不构成失败的观察项。
	// 混进 Problems 会让「提示」把退出码变成非 0，调用方就得靠字符串匹配区分。
	Hints     []string    `json:"hints,omitempty"`
	FrameZero *FrameStats `json:"frame_zero,omitempty"`
}

// ReportSchemaVersion 是 Report 的结构版本。
const ReportSchemaVersion = 1

// WriteJSON 原子写出报告。
func (r Report) WriteJSON(path string) error {
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(path, append(body, '\n'), 0o644)
}

// --- ffprobe / ffmpeg 调用 ---

// Toolchain 持有已解析的 ffprobe 路径和调用外部命令时使用的环境。
//
// 二进制路径在这里解析而不是用 exec.LookPath：安全模式下子进程的 PATH 是
// 受控白名单，按父进程 PATH 找到的二进制未必是同一个。
type Toolchain struct {
	ffprobe string
	path    string
	env     []string
}

// New 从给定环境的 PATH 里解析 ffprobe。
// 缺失时错误信息带 envutil.MissingToolPrefix，调用方据此返回退出码 127。
//
// ffmpeg 惰性解析：只读规格（Probe / CountFrames）用不上它，
// 在这里一并要求会让「装了 ffprobe 没装 ffmpeg」的机器连镜头校验都跑不了。
func New(env map[string]string) (Toolchain, error) {
	ffprobe, err := envutil.LookPath("ffprobe", env["PATH"])
	if err != nil {
		return Toolchain{}, err
	}
	return Toolchain{ffprobe: ffprobe, path: env["PATH"], env: envutil.EnvList(env)}, nil
}

// ffmpegPath 在真正需要 ffmpeg 的操作里解析它。
func (t Toolchain) ffmpegPath() (string, error) {
	return envutil.LookPath("ffmpeg", t.path)
}

type rawProbe struct {
	Streams []struct {
		CodecName      string `json:"codec_name"`
		CodecType      string `json:"codec_type"`
		Width          int    `json:"width"`
		Height         int    `json:"height"`
		PixFmt         string `json:"pix_fmt"`
		RFrameRate     string `json:"r_frame_rate"`
		AvgFrameRate   string `json:"avg_frame_rate"`
		NBFrames       string `json:"nb_frames"`
		NBReadFrames   string `json:"nb_read_frames"`
		SampleRate     string `json:"sample_rate"`
		Channels       int    `json:"channels"`
		ColorSpace     string `json:"color_space"`
		ColorPrimaries string `json:"color_primaries"`
		ColorTransfer  string `json:"color_transfer"`
		Duration       string `json:"duration"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// parse 把 ffprobe 的 JSON 输出解析成 Media。
//
// ffprobe 的字段类型是混着来的：width / height / channels 是数字，
// nb_frames / duration / sample_rate 是字符串。照直觉写解析必然出错，
// 所以这里逐个字段按实际类型声明，并由 realProbeOutput 那条测试守着。
func parse(body []byte, path string) (Media, error) {
	var raw rawProbe
	if err := json.Unmarshal(body, &raw); err != nil {
		return Media{}, fmt.Errorf("ffprobe 输出不是合法 JSON：%w", err)
	}
	media := Media{Path: path, DurationSeconds: parseFloat(raw.Format.Duration)}
	for _, stream := range raw.Streams {
		switch stream.CodecType {
		case "video":
			if media.Video != nil {
				continue
			}
			rate := stream.RFrameRate
			if parseRational(rate) == 0 {
				rate = stream.AvgFrameRate
			}
			num, den := splitRational(rate)
			frames := parseInt(stream.NBFrames)
			if frames == 0 {
				frames = parseInt(stream.NBReadFrames)
			}
			media.Video = &VideoStream{
				Codec:           stream.CodecName,
				WidthPx:         stream.Width,
				HeightPx:        stream.Height,
				PixelFormat:     stream.PixFmt,
				FPSNum:          num,
				FPSDen:          den,
				NBFrames:        frames,
				ColorSpace:      stream.ColorSpace,
				ColorPrimaries:  stream.ColorPrimaries,
				ColorTransfer:   stream.ColorTransfer,
				DurationSeconds: parseFloat(stream.Duration),
			}
		case "audio":
			if media.Audio != nil {
				continue
			}
			media.Audio = &AudioStream{
				Codec:           stream.CodecName,
				SampleRate:      parseInt(stream.SampleRate),
				Channels:        stream.Channels,
				DurationSeconds: parseFloat(stream.Duration),
			}
		}
	}
	// Video 和 Audio 都为 nil 说明 ffprobe 没认出任何可用流（通常是探测了非
	// 媒体文件），这种情况才报错。纯音频文件（如 dialogue 的 TTS 片段/静音/
	// 拼接产物）没有视频流是合法形态，Video 保持 nil 交给调用方按需处理——
	// Spec.Check 早就按这个约定写了（v == nil 时报「文件没有视频流」而不是
	// panic），只是 parse 一直没跟上，把这条合法路径提前堵死了。
	if media.Video == nil && media.Audio == nil {
		return Media{}, fmt.Errorf("%s 没有可识别的音视频流", path)
	}
	return media, nil
}

func parseFloat(value string) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0
	}
	return parsed
}

func parseInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func splitRational(value string) (int, int) {
	num, den, ok := strings.Cut(strings.TrimSpace(value), "/")
	if !ok {
		return 0, 0
	}
	n, errN := strconv.Atoi(num)
	d, errD := strconv.Atoi(den)
	if errN != nil || errD != nil || d == 0 {
		return 0, 0
	}
	return n, d
}

func parseRational(value string) float64 {
	n, d := splitRational(value)
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

// Probe 读取媒体文件的实测规格。不解码，只读容器和流头，代价与文件长度无关。
func (t Toolchain) Probe(path string) (Media, error) {
	cmd := exec.Command(t.ffprobe, "-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", path)
	cmd.Env = t.env
	output, err := cmd.Output()
	if err != nil {
		return Media{}, fmt.Errorf("无法读取媒体规格：%s（%s）", path, commandError(err))
	}
	return parse(output, path)
}

// CountFrames 解码整个文件精确统计帧数。
//
// 与 Probe 读到的 nb_frames 不同：容器声明的帧数可能缺失或不准，
// 而验收要断言的是「总帧数等于冻结时间轴的总帧数」。代价是完整解码一遍，
// 所以只在确实需要精确帧数时调用。
func (t Toolchain) CountFrames(path string) (int, error) {
	cmd := exec.Command(t.ffprobe, "-v", "error", "-count_frames",
		"-select_streams", "v:0", "-show_entries", "stream=nb_read_frames",
		"-print_format", "json", path)
	cmd.Env = t.env
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("无法统计帧数：%s（%s）", path, commandError(err))
	}
	var raw rawProbe
	if err := json.Unmarshal(output, &raw); err != nil {
		return 0, fmt.Errorf("ffprobe 帧数输出不是合法 JSON：%w", err)
	}
	for _, stream := range raw.Streams {
		if frames := parseInt(stream.NBReadFrames); frames > 0 {
			return frames, nil
		}
	}
	return 0, fmt.Errorf("ffprobe 未能统计出帧数：%s", path)
}

// Decode 完整解码一遍，确认文件没有截断或损坏。产物丢弃。
func (t Toolchain) Decode(path string) error {
	ffmpeg, err := t.ffmpegPath()
	if err != nil {
		return err
	}
	cmd := exec.Command(ffmpeg, "-v", "error", "-xerror", "-i", path, "-f", "null", "-")
	cmd.Env = t.env
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("无法完整解码：%s（%s）", path, firstLine(string(output)))
	}
	// -xerror 之外，ffmpeg 遇到部分错误仍会返回 0 但往 stderr 写内容。
	if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
		return fmt.Errorf("解码过程中报错：%s（%s）", path, firstLine(trimmed))
	}
	return nil
}

// ExtractFrame 抽取 atSeconds 处的一帧写成 PNG，自动建立目标目录。
func (t Toolchain) ExtractFrame(path string, atSeconds float64, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	// -ss 放在 -i 之前是关键帧快速定位，之后是精确定位。抽帧要的是「第 N 秒
	// 那一帧」而不是「附近的关键帧」，所以放在 -i 之后。
	ffmpeg, err := t.ffmpegPath()
	if err != nil {
		return err
	}
	cmd := exec.Command(ffmpeg, "-v", "error", "-y", "-i", path,
		"-ss", formatSeconds(atSeconds), "-frames:v", "1", "-f", "image2", "-c:v", "png", dest)
	cmd.Env = t.env
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("抽帧失败：%s @ %.3f 秒（%s）", path, atSeconds, firstLine(string(output)))
	}
	info, statErr := os.Stat(dest)
	if statErr != nil || info.Size() == 0 {
		// 时间点超出时长时 ffmpeg 会返回 0 但什么都不写。静默产出空文件
		// 比报错更危险：调用方会以为抽到了帧。
		_ = os.Remove(dest)
		return fmt.Errorf("抽帧没有产出内容：%s @ %.3f 秒（时间点可能超出视频时长）", path, atSeconds)
	}
	return nil
}

// FrameStats 是单帧的灰度统计量，用于判定黑帧和空白帧。
type FrameStats struct {
	AtSeconds float64 `json:"at_seconds"`
	Mean      float64 `json:"mean"`
	StdDev    float64 `json:"stddev"`
}

// 判定阈值。刻意取得极保守——误判「设计上就是深色的正常画面」为空白帧，
// 比漏判一个真空白帧更糟：前者会让人很快不再信任这个检查。
const (
	// flatStdDev 之下视为空白帧。
	//
	// 阈值不能凭「真实帧标准差通常很高」来定：深色背景上只有一行标题时，
	// 亮部占比可能不到 1%，标准差会低到 20 出头。取 2.0 意味着只拦几乎完全
	// 均匀的画面，把误判概率压到最低——纯色帧的标准差本来就接近 0。
	flatStdDev = 2.0
	// darkMean 之下视为整体过暗。这是提示信号而不是判定依据，见 Blank。
	darkMean = 16.0
)

// Flat 报告该帧是否几乎是纯色。黑、白和任何单色都算。
func (f FrameStats) Flat() bool { return f.StdDev < flatStdDev }

// Dark 报告该帧整体是否过暗。
//
// 这是提示而不是结论：深色背景配浅色标题是常见设计，整帧灰度均值可以很低，
// 但画面完全可读。所以 Dark 不参与 Blank 判定，只用于让人多看一眼。
func (f FrameStats) Dark() bool { return f.Mean < darkMean }

// Blank 报告该帧是否命中 frame.md 的 black_or_blank_frame_zero 禁令。
//
// 只看 Flat：纯黑帧的标准差本来就是 0，会被 Flat 抓到，无需再叠加亮度条件。
// 叠加了反而会把「深色背景 + 稀疏文字」的合法封面误判成空白帧。
func (f FrameStats) Blank() bool { return f.Flat() }

// statsSampleSize 是计算统计量时的缩放边长。
// 缩放到固定尺寸让阈值与画幅无关，也让代价与视频分辨率无关。
const statsSampleSize = 128

// FrameStats 抽取 atSeconds 处的一帧，缩放成灰度小图后在 Go 侧计算均值和标准差。
//
// 不用 ffmpeg 的 signalstats/blackdetect：它们把结果写进 stderr 的文本日志，
// 格式随版本变化，解析起来比自己算更脆。裸灰度像素是稳定接口。
func (t Toolchain) FrameStats(path string, atSeconds float64) (FrameStats, error) {
	ffmpeg, err := t.ffmpegPath()
	if err != nil {
		return FrameStats{}, err
	}
	// 输入端定位，理由同 ContactSheet：整片复查要对每个镜头交界各取两帧。
	cmd := exec.Command(ffmpeg, "-v", "error", "-ss", formatSeconds(atSeconds), "-i", path,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:%d,format=gray", statsSampleSize, statsSampleSize),
		"-f", "rawvideo", "-")
	cmd.Env = t.env
	output, err := cmd.Output()
	if err != nil {
		return FrameStats{}, fmt.Errorf("读取帧统计失败：%s @ %.3f 秒（%s）", path, atSeconds, commandError(err))
	}
	if len(output) == 0 {
		return FrameStats{}, fmt.Errorf("该时间点没有帧：%s @ %.3f 秒", path, atSeconds)
	}
	var sum float64
	for _, pixel := range output {
		sum += float64(pixel)
	}
	mean := sum / float64(len(output))
	var variance float64
	for _, pixel := range output {
		delta := float64(pixel) - mean
		variance += delta * delta
	}
	variance /= float64(len(output))
	return FrameStats{AtSeconds: atSeconds, Mean: mean, StdDev: math.Sqrt(variance)}, nil
}

func formatSeconds(value float64) string {
	return strconv.FormatFloat(value, 'f', 6, 64)
}

func commandError(err error) string {
	var exit *exec.ExitError
	if ok := asExitError(err, &exit); ok && len(exit.Stderr) > 0 {
		return firstLine(string(exit.Stderr))
	}
	return err.Error()
}

func asExitError(err error, target **exec.ExitError) bool {
	if exit, ok := err.(*exec.ExitError); ok {
		*target = exit
		return true
	}
	return false
}

func firstLine(value string) string {
	trimmed := strings.TrimSpace(value)
	if index := strings.IndexByte(trimmed, '\n'); index >= 0 {
		trimmed = trimmed[:index]
	}
	if len(trimmed) > 200 {
		trimmed = trimmed[:200]
	}
	if trimmed == "" {
		return "无输出"
	}
	return trimmed
}

// VerifyOptions 描述一次完整校验除规格比对外还要做哪些检查。
//
// 三项都默认关闭，因为它们的代价和 Probe 不在一个量级：
// ExactFrames 与 Decode 都要完整解码一遍文件，CheckFrameZero 要解一帧。
type VerifyOptions struct {
	Spec Spec
	// ExactFrames 用 CountFrames 解码统计精确帧数，取代容器声明的 nb_frames。
	// 需要断言「总帧数等于冻结时间轴」时必须打开：容器声明可能缺失或不准。
	ExactFrames bool
	// Decode 完整解码一遍，确认文件没有截断或损坏。
	Decode bool
	// CheckFrameZero 检查第 0 帧是否是空白帧，对应 frame.md 的
	// black_or_blank_frame_zero 禁令。
	CheckFrameZero bool
}

// Verify 跑完整的校验流程并返回机器可读报告。
//
// 返回的 error 只表示「检查没跑成」（文件读不了、工具缺失），
// 「检查跑了但不合格」体现为 Report.OK 为 false 和 Problems 非空——
// 两者对调用方的含义完全不同，混在一起会让退出码失去区分度。
func (t Toolchain) Verify(path string, opts VerifyOptions) (Report, error) {
	if err := opts.Spec.Validate(); err != nil {
		return Report{}, err
	}
	media, err := t.Probe(path)
	if err != nil {
		return Report{}, err
	}
	report := Report{SchemaVersion: ReportSchemaVersion, Path: path, Media: media}

	// media.Video == nil 时（纯音频文件）跳过整个精确帧数统计：既没有视频流
	// 可数，硬调用 CountFrames 也只会拿到「未能统计出帧数」这种无意义的错误，
	// 不如直接放行到下面的 Spec.Check，由它给出「文件没有视频流」这条可读问题。
	if opts.ExactFrames && media.Video != nil {
		frames, err := t.CountFrames(path)
		if err != nil {
			return Report{}, err
		}
		// 用精确值覆盖容器声明，让 Check 与报告读到的是同一个数。
		media.Video.NBFrames = frames
		report.Media = media
	}
	report.Problems = opts.Spec.Check(media)

	if opts.Decode {
		if err := t.Decode(path); err != nil {
			report.Problems = append(report.Problems, err.Error())
		}
	}
	if opts.CheckFrameZero {
		stats, err := t.FrameStats(path, 0)
		if err != nil {
			return Report{}, err
		}
		report.FrameZero = &stats
		if stats.Blank() {
			report.Problems = append(report.Problems,
				fmt.Sprintf("第 0 帧是空白帧，不能作为封面：灰度均值 %.1f、标准差 %.2f", stats.Mean, stats.StdDev))
		} else if stats.Dark() {
			// 只提示不判失败：深色背景配浅色标题是常见设计。
			report.Hints = append(report.Hints,
				fmt.Sprintf("第 0 帧整体偏暗（灰度均值 %.1f），确认标题在目标平台可读", stats.Mean))
		}
	}
	report.OK = len(report.Problems) == 0
	return report, nil
}

// ExtractedFrame 是一次抽帧的结果。
type ExtractedFrame struct {
	AtSeconds float64    `json:"at_seconds"`
	Image     string     `json:"image"`
	Stats     FrameStats `json:"stats"`
	Blank     bool       `json:"blank"`
	Dark      bool       `json:"dark"`
}

// FrameReport 是一次抽帧检查的机器可读结果。
type FrameReport struct {
	SchemaVersion int              `json:"schema_version"`
	Path          string           `json:"path"`
	OK            bool             `json:"ok"`
	Frames        []ExtractedFrame `json:"frames"`
	Problems      []string         `json:"problems"`
	Hints         []string         `json:"hints,omitempty"`
}

// WriteJSON 原子写出抽帧报告。
func (r FrameReport) WriteJSON(path string) error {
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(path, append(body, '\n'), 0o644)
}

// ExtractFrames 按给定时间点批量抽帧到 destDir，并统计每帧的灰度分布。
//
// 抽帧本身不判定对错——画面对不对只有能看图的一方说了算。这个方法的职责是
// 把「渲染完的 mp4」变成「可以逐张看的 PNG」，顺带把纯色帧这种机器能判的
// 情况标出来，省得靠人眼去发现一张全黑的图。
func (t Toolchain) ExtractFrames(path string, atSeconds []float64, destDir string) (FrameReport, error) {
	report := FrameReport{SchemaVersion: ReportSchemaVersion, Path: path}
	for _, at := range atSeconds {
		image := filepath.Join(destDir, fmt.Sprintf("frame-%s.png", strings.ReplaceAll(formatSecondsShort(at), ".", "_")))
		if err := t.ExtractFrame(path, at, image); err != nil {
			return FrameReport{}, err
		}
		stats, err := t.FrameStats(path, at)
		if err != nil {
			return FrameReport{}, err
		}
		frame := ExtractedFrame{AtSeconds: at, Image: image, Stats: stats, Blank: stats.Blank(), Dark: stats.Dark()}
		report.Frames = append(report.Frames, frame)
		if frame.Blank {
			report.Problems = append(report.Problems,
				fmt.Sprintf("%.3f 秒处是空白帧：灰度均值 %.1f、标准差 %.2f（%s）", at, stats.Mean, stats.StdDev, image))
		} else if frame.Dark {
			report.Hints = append(report.Hints,
				fmt.Sprintf("%.3f 秒处整体偏暗（灰度均值 %.1f），确认内容可读（%s）", at, stats.Mean, image))
		}
	}
	report.OK = len(report.Problems) == 0
	return report, nil
}

// formatSecondsShort 生成用于文件名的时间戳，去掉尾随零。
func formatSecondsShort(value float64) string {
	text := strconv.FormatFloat(value, 'f', 3, 64)
	text = strings.TrimRight(text, "0")
	return strings.TrimSuffix(text, ".")
}
