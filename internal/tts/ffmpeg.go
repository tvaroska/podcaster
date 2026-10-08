package tts

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Probe reports duration (seconds) of an audio file using ffprobe/ffmpeg.
func ProbeDuration(ctx context.Context, ffmpegBin, path string) (float64, error) {
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	if _, err := exec.LookPath(ffmpegBin); err != nil {
		return 0, err
	}
	probe := "ffprobe"
	if dir := filepath.Dir(ffmpegBin); dir != "." && dir != "" {
		candidate := filepath.Join(dir, "ffprobe")
		if _, err := os.Stat(candidate); err == nil {
			probe = candidate
		}
	}
	cmd := exec.CommandContext(ctx, probe,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Fallback: ffmpeg -i prints duration on stderr.
		return probeWithFFmpeg(ctx, ffmpegBin, path)
	}
	s := strings.TrimSpace(stdout.String())
	d, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("parse duration %q: %w", s, err)
	}
	return d, nil
}

func probeWithFFmpeg(ctx context.Context, ffmpegBin, path string) (float64, error) {
	cmd := exec.CommandContext(ctx, ffmpegBin, "-i", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	out := stderr.String()
	idx := strings.Index(out, "Duration: ")
	if idx < 0 {
		return 0, fmt.Errorf("ffmpeg did not report duration: %s", strings.TrimSpace(out))
	}
	rest := out[idx+len("Duration: "):]
	end := strings.IndexByte(rest, ',')
	if end < 0 {
		end = len(rest)
	}
	hms := strings.TrimSpace(rest[:end])
	var h, m int
	var s float64
	if _, err := fmt.Sscanf(hms, "%d:%d:%f", &h, &m, &s); err != nil {
		return 0, fmt.Errorf("parse duration %q: %w", hms, err)
	}
	return float64(h*3600+m*60) + s, nil
}

// ConcatWAV concatenates WAV files with ffmpeg concat demuxer.
func ConcatWAV(ctx context.Context, ffmpegBin string, inputs []string, dest string) error {
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	if len(inputs) == 0 {
		return fmt.Errorf("no wav inputs")
	}
	if len(inputs) == 1 {
		in, err := os.Open(inputs[0])
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(dest)
		if err != nil {
			return err
		}
		_, copyErr := out.ReadFrom(in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	list, err := os.CreateTemp("", "podcaster-concat-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(list.Name())
	for _, in := range inputs {
		abs, err := filepath.Abs(in)
		if err != nil {
			_ = list.Close()
			return err
		}
		if _, err := fmt.Fprintf(list, "file '%s'\n", escapeConcat(abs)); err != nil {
			_ = list.Close()
			return err
		}
	}
	if err := list.Close(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, ffmpegBin,
		"-y", "-f", "concat", "-safe", "0",
		"-i", list.Name(),
		"-c", "copy",
		dest,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg concat: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func escapeConcat(path string) string {
	return strings.ReplaceAll(path, "'", `'\''`)
}

// BroadcastAudioFilter normalizes speech to the -16 LUFS podcast standard with
// a gentle 80 Hz high-pass and 2:1 dynamic compression.
const BroadcastAudioFilter = "highpass=f=80,acompressor=threshold=-18dB:ratio=2:attack=20:release=250,loudnorm=I=-16:TP=-1.5:LRA=11"

// EncodeMP3 converts a WAV (or any ffmpeg-readable) file to 128kbps mono 24kHz
// MP3 with broadcast loudness mastering.
func EncodeMP3(ctx context.Context, ffmpegBin, src, dest string) error {
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, ffmpegBin,
		"-y", "-i", src,
		"-af", BroadcastAudioFilter,
		"-codec:a", "libmp3lame",
		"-b:a", "128k",
		"-ac", "1",
		"-ar", "24000",
		dest,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg mp3: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
