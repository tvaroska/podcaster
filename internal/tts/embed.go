package tts

import _ "embed"

//go:embed testdata/beep.mp3
var embeddedBeep []byte

func defaultMockMP3() []byte {
	return embeddedBeep
}
