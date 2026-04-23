package vm

import (
	"fmt"
	"runtime"
)

func (v *VM) cmdTTS(rest string) []Result {
	text, _ := splitBraceArg(rest)
	if text == "" {
		text = rest
	}

	if text == "" {
		return echoResults([]string{"#tts: usage: #tts {text}"})
	}

	if runtime.GOOS != "darwin" {
		return echoResults([]string{fmt.Sprintf("#tts: speech not supported on %s", runtime.GOOS)})
	}

	if v.ttsFn != nil {
		v.ttsFn(text)
	}

	return nil // Silent success on macOS
}
