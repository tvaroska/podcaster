package tts

import (
	"encoding/binary"
	"os"
	"time"
)

const (
	// SentenceSilenceDuration is the standard 300ms pause injected between sentences.
	SentenceSilenceDuration = 300 * time.Millisecond
	// ParagraphSilenceDuration is the 700ms pause injected between paragraphs/sections.
	ParagraphSilenceDuration = 700 * time.Millisecond
	// TailSilenceDuration is the gentle 250ms trailing silence at the end of an episode.
	TailSilenceDuration = 250 * time.Millisecond
	// BoundaryFadeDuration is the 8ms linear ramp applied at speech boundaries to prevent zero-crossing clicks.
	BoundaryFadeDuration = 8 * time.Millisecond
)

// PolishChunkWAV applies an 8ms linear fade-in/fade-out to prevent boundary clicks,
// expands short internal inter-sentence pauses to 300ms, and appends the requested
// trailing digital silence (300ms for sentence breaks, 700ms for paragraph/section breaks).
//
// If path does not contain a standard 16-bit mono PCM RIFF WAVE header (for example,
// synthetic text written by fake test scripts), PolishChunkWAV is a no-op.
func PolishChunkWAV(path string, trailingSilence time.Duration) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sampleRate, samples, ok := parsePCM16MonoWAV(raw)
	if !ok || len(samples) == 0 || sampleRate <= 0 {
		return nil
	}

	samples = trimEdgeSilence(samples, sampleRate)
	if len(samples) == 0 {
		return nil
	}

	fadeSamples := durationToSamples(sampleRate, BoundaryFadeDuration)
	applyLinearFadeIn(samples, fadeSamples)
	applyLinearFadeOut(samples, fadeSamples)

	samples = expandInternalSentencePauses(samples, sampleRate, SentenceSilenceDuration, fadeSamples)

	if trailingSilence > 0 {
		pad := durationToSamples(sampleRate, trailingSilence)
		if pad > 0 {
			samples = append(samples, make([]int16, pad)...)
		}
	}

	out := encodePCM16MonoWAV(sampleRate, samples)
	return os.WriteFile(path, out, 0o644)
}

func parsePCM16MonoWAV(b []byte) (sampleRate int, samples []int16, ok bool) {
	if len(b) < 44 {
		return 0, nil, false
	}
	if string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return 0, nil, false
	}
	pos := 12
	var (
		foundFmt  bool
		audioFmt  uint16
		channels  uint16
		rate      uint32
		bits      uint16
		dataBytes []byte
	)
	for pos+8 <= len(b) {
		chunkID := string(b[pos : pos+4])
		chunkSize := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		pos += 8
		if chunkSize < 0 || pos+chunkSize > len(b) {
			return 0, nil, false
		}
		payload := b[pos : pos+chunkSize]
		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return 0, nil, false
			}
			audioFmt = binary.LittleEndian.Uint16(payload[0:2])
			channels = binary.LittleEndian.Uint16(payload[2:4])
			rate = binary.LittleEndian.Uint32(payload[4:8])
			bits = binary.LittleEndian.Uint16(payload[14:16])
			foundFmt = true
		case "data":
			dataBytes = payload
		}
		pos += chunkSize
		if chunkSize%2 == 1 && pos < len(b) {
			pos++
		}
	}
	if !foundFmt || audioFmt != 1 || channels != 1 || bits != 16 || rate == 0 || len(dataBytes) < 2 {
		return 0, nil, false
	}
	n := len(dataBytes) / 2
	samples = make([]int16, n)
	for i := 0; i < n; i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(dataBytes[2*i : 2*i+2]))
	}
	return int(rate), samples, true
}

func encodePCM16MonoWAV(sampleRate int, samples []int16) []byte {
	dataSize := len(samples) * 2
	buf := make([]byte, 44+dataSize)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], 1) // Mono
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(buf[32:34], 2)  // BlockAlign
	binary.LittleEndian.PutUint16(buf[34:36], 16) // BitsPerSample
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[44+2*i:44+2*i+2], uint16(s))
	}
	return buf
}

func durationToSamples(sampleRate int, d time.Duration) int {
	return int((int64(sampleRate) * d.Microseconds()) / 1_000_000)
}

func abs16(v int16) int {
	if v < 0 {
		return -int(v)
	}
	return int(v)
}

// trimEdgeSilence trims leading and trailing dead air (|s| <= 64, ~ -54 dBFS)
// while keeping a 15ms natural margin so plosives and breath tails are never clipped.
func trimEdgeSilence(samples []int16, sampleRate int) []int16 {
	const threshold = 64
	margin := durationToSamples(sampleRate, 15*time.Millisecond)
	first := -1
	last := -1
	for i, s := range samples {
		if abs16(s) > threshold {
			if first == -1 {
				first = i
			}
			last = i
		}
	}
	if first == -1 {
		return samples
	}
	start := first - margin
	if start < 0 {
		start = 0
	}
	end := last + margin + 1
	if end > len(samples) {
		end = len(samples)
	}
	return samples[start:end]
}

func applyLinearFadeIn(samples []int16, fadeSamples int) {
	if fadeSamples <= 0 {
		return
	}
	if fadeSamples > len(samples) {
		fadeSamples = len(samples)
	}
	for i := 0; i < fadeSamples; i++ {
		samples[i] = int16((int64(samples[i]) * int64(i)) / int64(fadeSamples))
	}
}

func applyLinearFadeOut(samples []int16, fadeSamples int) {
	if fadeSamples <= 0 {
		return
	}
	if fadeSamples > len(samples) {
		fadeSamples = len(samples)
	}
	n := len(samples)
	for i := 0; i < fadeSamples; i++ {
		samples[n-1-i] = int16((int64(samples[n-1-i]) * int64(i)) / int64(fadeSamples))
	}
}

// expandInternalSentencePauses detects inter-sentence silence valleys (>= 75ms of
// near-zero signal <= 180 amplitude) surrounded by at least 250ms of active speech,
// fades the edges over fadeSamples, and pads them to targetPause (300ms).
func expandInternalSentencePauses(samples []int16, sampleRate int, targetPause time.Duration, fadeSamples int) []int16 {
	const silenceThreshold = 180 // ~ -45 dBFS
	minSilence := durationToSamples(sampleRate, 75*time.Millisecond)
	targetSamples := durationToSamples(sampleRate, targetPause)
	minVoiceMargin := durationToSamples(sampleRate, 250*time.Millisecond)
	if targetSamples <= minSilence || len(samples) < 2*minVoiceMargin+minSilence {
		return samples
	}

	type span struct {
		start, end int
	}
	var pauses []span
	runStart := -1
	for i := minVoiceMargin; i < len(samples)-minVoiceMargin; i++ {
		if abs16(samples[i]) <= silenceThreshold {
			if runStart == -1 {
				runStart = i
			}
		} else if runStart != -1 {
			runLen := i - runStart
			if runLen >= minSilence && runLen < targetSamples {
				pauses = append(pauses, span{start: runStart, end: i})
			}
			runStart = -1
		}
	}
	if runStart != -1 {
		runLen := (len(samples) - minVoiceMargin) - runStart
		if runLen >= minSilence && runLen < targetSamples {
			pauses = append(pauses, span{start: runStart, end: len(samples) - minVoiceMargin})
		}
	}
	if len(pauses) == 0 {
		return samples
	}

	var out []int16
	cursor := 0
	for _, p := range pauses {
		mid := (p.start + p.end) / 2
		leftSeg := append([]int16(nil), samples[cursor:mid]...)
		if cursor > 0 {
			applyLinearFadeIn(leftSeg, fadeSamples)
		}
		applyLinearFadeOut(leftSeg, fadeSamples)
		out = append(out, leftSeg...)

		extra := targetSamples - (p.end - p.start)
		if extra > 0 {
			out = append(out, make([]int16, extra)...)
		}
		cursor = mid
	}
	tailSeg := append([]int16(nil), samples[cursor:]...)
	applyLinearFadeIn(tailSeg, fadeSamples)
	out = append(out, tailSeg...)
	return out
}
