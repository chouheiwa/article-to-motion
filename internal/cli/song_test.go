package cli

import (
	"bytes"
	"fmt"
	assets "github.com/chouheiwa/articale-to-motion"
	"github.com/chouheiwa/articale-to-motion/internal/preset"
	"github.com/chouheiwa/articale-to-motion/internal/scene"
	"github.com/chouheiwa/articale-to-motion/internal/schedule"
	"github.com/chouheiwa/articale-to-motion/internal/song"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSongInitModes(t *testing.T) {
	for _, delivery := range []string{"speech", "song"} {
		root := filepath.Join(t.TempDir(), "project")
		var out, err bytes.Buffer
		code := Execute([]string{"init", root, "--canvas", "vertical-3x4", "--skip-hyperframes", "--delivery", delivery}, &out, &err)
		if code != 0 {
			t.Fatal(err.String())
		}
		_, e := os.Stat(filepath.Join(root, "song.yaml"))
		if (e == nil) != (delivery == "song") {
			t.Fatal(delivery, e)
		}
	}
	root := filepath.Join(t.TempDir(), "bad")
	var out, err bytes.Buffer
	if Execute([]string{"init", root, "--canvas", "vertical-3x4", "--skip-hyperframes", "--delivery", "song", "--narration", "cast"}, &out, &err) == 0 {
		t.Fatal("cast song accepted")
	}
	if _, e := os.Stat(root); !os.IsNotExist(e) {
		t.Fatal("partial scaffold")
	}
}

func TestSongCuesAndValidationBothCanvases(t *testing.T) {
	for _, canvas := range []string{"vertical-3x4", "vertical-9x16"} {
		t.Run(canvas, func(t *testing.T) {
			root := t.TempDir()
			var out bytes.Buffer
			if code := Execute([]string{"init", root, "--canvas", canvas, "--style", preset.DefaultStyle().ID, "--skip-hyperframes", "--delivery", "song"}, &out, &out); code != 0 {
				t.Fatal(out.String())
			}
			dir := filepath.Join(root, song.Store, "candidates", "chosen")
			os.MkdirAll(dir, 0755)
			os.WriteFile(filepath.Join(dir, "audio.mp3"), []byte("audio"), 0644)
			os.WriteFile(filepath.Join(dir, "lyrics.txt"), []byte("歌词\n歌词"), 0644)
			c := song.Candidate{ID: "chosen", State: "ready", Audio: "audio.mp3", Lyrics: "lyrics.txt", Duration: 3.01}
			c.AudioSHA, _ = song.HashFile(filepath.Join(dir, c.Audio))
			c.LyricsSHA, _ = song.HashFile(filepath.Join(dir, c.Lyrics))
			song.WriteJSON(filepath.Join(dir, "candidate.json"), c)
			if e := song.Select(root, c.ID); e != nil {
				t.Fatal(e)
			}
			tl := song.Timeline{Schema: 1, CandidateID: c.ID, AudioSHA: c.AudioSHA, LyricsSHA: c.LyricsSHA, Duration: c.Duration, Reviewed: true, Lines: []song.Line{{ID: "line-1", Text: "歌词", Start: 0.3, End: 1.8}, {ID: "line-2", Text: "歌词", Start: 2.1, End: 2.8}}, Beats: []float64{0.3, 1, 2, 3}}
			if e := song.SaveTimeline(root, tl); e != nil {
				t.Fatal(e)
			}
			scenesRoot := filepath.Join(root, "scenes")
			for i, duration := range []float64{1.5, 46.0 / 30} {
				sd := filepath.Join(scenesRoot, fmt.Sprintf("scene-%03d", i+1))
				os.MkdirAll(sd, 0755)
				os.WriteFile(filepath.Join(sd, "transcript.srt"), []byte(""), 0644)
				frame, _ := os.ReadFile(filepath.Join(root, "frame.md"))
				os.WriteFile(filepath.Join(sd, "frame.md"), frame, 0644)
				song.WriteJSON(filepath.Join(sd, "scene.json"), map[string]any{"id": fmt.Sprintf("scene-%03d", i+1), "duration_seconds": duration, "text": "歌词", "output": "out.mp4", "transcript": "transcript.srt", "style_guide": "frame.md"})
			}
			if e := applySongCues(root, scenesRoot, &out); e != nil {
				t.Fatal(e)
			}
			if code := Execute([]string{"validate", "song", "--project-root", root}, &out, &out); code != 0 {
				t.Fatal(out.String())
			}
			scenes, e := schedule.Plan(scenesRoot)
			if e != nil {
				t.Fatal(e)
			}
			p, e := scene.BuildPrompt(scenes[0], map[string]string{scene.SongExplainerSkillName: filepath.Join(root, ".agents", "skills")})
			if e != nil || !strings.Contains(p, "song-explainer") || !strings.Contains(p, "不得生成或替换歌曲") {
				t.Fatal(p, e)
			}
			var local song.Timeline
			song.ReadJSON(filepath.Join(scenes[1].Directory, "song-cues.json"), &local)
			if local.Lines[0].Start != 0 || math.Abs(local.Lines[0].End-0.3) > 1e-6 {
				t.Fatal(local)
			}
			st, _ := os.Stat(filepath.Join(scenes[0].Directory, "scene.json"))
			if e = applySongCues(root, scenesRoot, &out); e != nil {
				t.Fatal(e)
			}
			st2, _ := os.Stat(filepath.Join(scenes[0].Directory, "scene.json"))
			if !st.ModTime().Equal(st2.ModTime()) {
				t.Fatal("idempotent cues changed mtime")
			}
			for _, item := range scenes {
				os.WriteFile(item.OutputPath(), []byte("render"), 0644)
			}
			if e = songOutputsCurrent(scenes, ""); e != nil {
				t.Fatal(e)
			}
			tl.Lines[0].Start = 0.4
			song.SaveTimeline(root, tl)
			if e = scene.VerifySongInputs(scenes[0]); e == nil {
				t.Fatal("stale song allowed")
			}
			if code := Execute([]string{"validate", "song", "--project-root", root}, &out, &out); code == 0 {
				t.Fatal("stale validate")
			}
			if e = applySongCues(root, scenesRoot, &out); e != nil {
				t.Fatal(e)
			}
			updated, e := schedule.Plan(scenesRoot)
			if e != nil {
				t.Fatal(e)
			}
			if e = songOutputsCurrent(updated, ""); e == nil {
				t.Fatal("stale render accepted by concat guard")
			}
			// A coverage failure must not partially update any scene.
			before, _ := os.ReadFile(filepath.Join(scenes[0].Directory, "scene.json"))
			last := scenes[1]
			last.DurationSeconds = 1
			song.WriteJSON(filepath.Join(last.Directory, "scene.json"), last)
			if e = applySongCues(root, scenesRoot, &out); e == nil {
				t.Fatal("missing outro")
			}
			after, _ := os.ReadFile(filepath.Join(scenes[0].Directory, "scene.json"))
			if !bytes.Equal(before, after) {
				t.Fatal("partial update")
			}
		})
	}
}
func TestSongScaffoldConflictDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "PROMPT.md"), []byte("my prompt"), 0644)
	os.WriteFile(filepath.Join(root, "PROMPT-SONG.md"), []byte("custom song"), 0644)
	if e := songScaffold(root); e == nil {
		t.Fatal("must reject conflict")
	}
	if _, e := os.Stat(filepath.Join(root, "song.yaml")); !os.IsNotExist(e) {
		t.Fatal("partial config")
	}
	os.Remove(filepath.Join(root, "PROMPT-SONG.md"))
	if e := songScaffold(root); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(root, "PROMPT.md"))
	if string(b) != "my prompt" {
		t.Fatal("user prompt overwritten")
	}
	if e := songScaffold(root); e != nil {
		t.Fatal("idempotent", e)
	}
}

func TestRunSongAlwaysAddsContract(t *testing.T) {
	root := t.TempDir()
	if e := songScaffold(root); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(root, "article-to-motion.conf"), []byte("ORCHESTRATOR=codex\nRENDERER=claude\n"), 0644)
	os.WriteFile(filepath.Join(root, "custom.md"), []byte("CUSTOM-PROMPT"), 0644)
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\n/bin/cat\n"), 0755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ORCHESTRATOR", "codex")
	t.Chdir(root)
	for _, args := range [][]string{{"run"}, {"run", "custom.md"}} {
		var out, err bytes.Buffer
		if code := Execute(args, &out, &err); code != 0 {
			t.Fatal(err.String())
		}
		if !strings.Contains(out.String(), "歌曲模式必要契约") || !strings.Contains(out.String(), "不得代替用户选择") {
			t.Fatal(out.String())
		}
		if len(args) > 1 && !strings.Contains(out.String(), "CUSTOM-PROMPT") {
			t.Fatal("lost explicit prompt")
		}
	}
}

func TestRunAllSongAndSRTMutuallyExclusive(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "scenes"), 0755)
	var out bytes.Buffer
	code := Execute([]string{"scene", "run-all", filepath.Join(root, "scenes"), "--srt", "sub.srt", "--song-timeline", "production/song/timeline.json"}, &out, &out)
	if code == 0 || !strings.Contains(out.String(), "互斥") {
		t.Fatal(code, out.String())
	}
}

func TestSongPromptRequiresWebHandoffPause(t *testing.T) {
	shared, e := assets.Shared()
	if e != nil {
		t.Fatal(e)
	}
	b, e := fs.ReadFile(shared, "PROMPT-SONG.md")
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"am song handoff", "am song receive", "waiting_for_audio", "此处必须暂停等待用户回复", "不轮询目录", "完整歌词和曲风", "FINAL_LYRICS"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("song prompt missing %q", want)
		}
	}
}
