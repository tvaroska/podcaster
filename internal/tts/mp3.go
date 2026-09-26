package tts

// mp3DurationSeconds estimates duration from MPEG frames.
// Prefers Xing/Info frame count when present, otherwise CBR from bitrate.
func mp3DurationSeconds(b []byte) float64 {
	b = skipID3(b)
	off := findMPEGSync(b)
	if off < 0 || off+4 > len(b) {
		return 0
	}
	hdr := b[off : off+4]
	version, layer, br, sr, _, channels := parseMPEGHeader(hdr)
	if br == 0 || sr == 0 {
		return 0
	}
	samples := samplesPerFrame(version, layer)
	if n := xingFrames(b[off:], version, channels); n > 0 && samples > 0 {
		return float64(n) * float64(samples) / float64(sr)
	}
	payload := len(b) - off
	if payload <= 0 {
		return 0
	}
	return float64(payload) * 8 / float64(br)
}

func skipID3(b []byte) []byte {
	if len(b) < 10 || string(b[:3]) != "ID3" {
		return b
	}
	size := int(b[6]&0x7f)<<21 | int(b[7]&0x7f)<<14 | int(b[8]&0x7f)<<7 | int(b[9]&0x7f)
	end := 10 + size
	if end > len(b) || end < 10 {
		return b
	}
	return b[end:]
}

func findMPEGSync(b []byte) int {
	for i := 0; i+1 < len(b); i++ {
		if b[i] == 0xff && b[i+1]&0xe0 == 0xe0 {
			if i+4 <= len(b) {
				_, layer, br, sr, _, _ := parseMPEGHeader(b[i : i+4])
				if layer != 0 && br != 0 && sr != 0 {
					return i
				}
			}
		}
	}
	return -1
}

func parseMPEGHeader(h []byte) (version, layer, bitrate, sampleRate, padding, channels int) {
	if len(h) < 4 {
		return
	}
	verIdx := int(h[1] >> 3 & 0x03)
	layerIdx := int(h[1] >> 1 & 0x03)
	brIdx := int(h[2] >> 4 & 0x0f)
	srIdx := int(h[2] >> 2 & 0x03)
	padding = int(h[2] >> 1 & 0x01)
	chIdx := int(h[3] >> 6 & 0x03)

	switch verIdx {
	case 3:
		version = 1
	case 2:
		version = 2
	case 0:
		version = 25
	default:
		return
	}
	switch layerIdx {
	case 1:
		layer = 3
	case 2:
		layer = 2
	case 3:
		layer = 1
	default:
		return
	}
	bitrate = mpegBitrate(version, layer, brIdx)
	sampleRate = mpegSampleRate(version, srIdx)
	channels = 2
	if chIdx == 3 {
		channels = 1
	}
	return
}

func samplesPerFrame(version, layer int) int {
	if layer == 1 {
		return 384
	}
	if layer == 3 && version != 1 {
		return 576
	}
	return 1152
}

func mpegBitrate(version, layer, idx int) int {
	if idx <= 0 || idx >= 15 {
		return 0
	}
	var table [15]int
	switch {
	case version == 1 && layer == 3:
		table = [15]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	case version == 1 && layer == 2:
		table = [15]int{0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384}
	case version == 1 && layer == 1:
		table = [15]int{0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448}
	default:
		// MPEG-2 / 2.5 layer 3
		table = [15]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
	}
	return table[idx] * 1000
}

func mpegSampleRate(version, idx int) int {
	if idx > 2 {
		return 0
	}
	switch version {
	case 1:
		return []int{44100, 48000, 32000}[idx]
	case 2:
		return []int{22050, 24000, 16000}[idx]
	default:
		return []int{11025, 12000, 8000}[idx]
	}
}

func xingFrames(frame []byte, version, channels int) int {
	side := 17
	if version == 1 {
		if channels == 1 {
			side = 17
		} else {
			side = 32
		}
	} else if channels != 1 {
		side = 9
	}
	off := 4 + side
	if off+8 > len(frame) {
		return 0
	}
	tag := string(frame[off : off+4])
	if tag != "Xing" && tag != "Info" {
		return 0
	}
	flags := uint32(frame[off+4])<<24 | uint32(frame[off+5])<<16 | uint32(frame[off+6])<<8 | uint32(frame[off+7])
	if flags&0x1 == 0 || off+12 > len(frame) {
		return 0
	}
	n := int(uint32(frame[off+8])<<24 | uint32(frame[off+9])<<16 | uint32(frame[off+10])<<8 | uint32(frame[off+11]))
	if n <= 0 {
		return 0
	}
	return n
}
