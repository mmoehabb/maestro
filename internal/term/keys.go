package term

import (
	"fmt"
	"strconv"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
)

var navigation = map[rune]string{uv.KeyUp: "A", uv.KeyDown: "B", uv.KeyRight: "C", uv.KeyLeft: "D", uv.KeyHome: "H", uv.KeyEnd: "F"}

// encodeEnhanced implements the negotiated kitty flags. Plain Enter deliberately
// stays CR, even in enhanced mode, as required by Maestro's input contract.
func encodeEnhanced(k uv.Key, release bool, flags int) (string, bool) {
	if k.Code == uv.KeyEnter && k.Mod == 0 {
		if release {
			return "", true
		}
		return "\r", true
	}
	if release && flags&2 == 0 {
		return "", true
	}
	if flags == 0 {
		return "", release
	}
	if !release && flags&8 == 0 && k.Mod & ^(uv.ModShift|uv.ModCapsLock|uv.ModNumLock) == 0 && k.Text != "" {
		return k.Text, true
	}
	code := k.Code
	switch code {
	case uv.KeyEscape:
		code = 27
	case uv.KeyTab:
		code = 9
	case uv.KeyBackspace:
		code = 127
	case uv.KeyEnter:
		code = 13
	}
	// Kitty's modifier bit order differs from ultraviolet for super and meta.
	mod := 1
	for i, flag := range []uv.KeyMod{uv.ModShift, uv.ModAlt, uv.ModCtrl, uv.ModSuper, uv.ModHyper, uv.ModMeta, uv.ModCapsLock, uv.ModNumLock} {
		if k.Mod&flag != 0 {
			mod += 1 << i
		}
	}
	mods := strconv.Itoa(mod)
	if flags&2 != 0 {
		kind := 1
		if release {
			kind = 3
		} else if k.IsRepeat {
			kind = 2
		}
		mods += ":" + strconv.Itoa(kind)
	}
	if suffix, ok := navigation[code]; ok {
		return "\x1b[1;" + mods + suffix, true
	}
	if n, ok := map[rune]int{uv.KeyInsert: 2, uv.KeyDelete: 3, uv.KeyPgUp: 5, uv.KeyPgDown: 6, uv.KeyF5: 15, uv.KeyF6: 17, uv.KeyF7: 18, uv.KeyF8: 19, uv.KeyF9: 20, uv.KeyF10: 21, uv.KeyF11: 23, uv.KeyF12: 24}[code]; ok {
		return fmt.Sprintf("\x1b[%d;%s~", n, mods), true
	}
	if code >= uv.KeyF1 && code <= uv.KeyF4 {
		return fmt.Sprintf("\x1b[1;%s%c", mods, 'P'+code-uv.KeyF1), true
	}
	if code < 0 || code > 0x10ffff {
		return "", true
	}
	keys := strconv.Itoa(int(code))
	if flags&4 != 0 && (k.ShiftedCode != 0 || k.BaseCode != 0) {
		keys += ":"
		if k.ShiftedCode != 0 {
			keys += strconv.Itoa(int(k.ShiftedCode))
		}
		if k.BaseCode != 0 {
			keys += ":" + strconv.Itoa(int(k.BaseCode))
		}
	}
	text := ""
	if flags&16 != 0 && !release && k.Text != "" {
		parts := []string{}
		for _, r := range k.Text {
			parts = append(parts, strconv.Itoa(int(r)))
		}
		text = ";" + strings.Join(parts, ":")
	}
	return "\x1b[" + keys + ";" + mods + text + "u", true
}
