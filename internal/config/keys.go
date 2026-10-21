package config

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func splitKey(key string) (map[string]bool, string) {
	modifiers := map[string]bool{}
	for {
		found := false
		for _, modifier := range []string{"ctrl", "alt", "shift", "super"} {
			if strings.HasPrefix(key, modifier+"+") {
				modifiers[modifier] = true
				key = strings.TrimPrefix(key, modifier+"+")
				found = true
				break
			}
		}
		if !found {
			return modifiers, key
		}
	}
}

// CanonicalKey normalizes modifier order and equivalent shifted characters.
func CanonicalKey(key string) string {
	modifiers, base := splitKey(key)
	if modifiers["shift"] && utf8.RuneCountInString(base) == 1 {
		char, _ := utf8.DecodeRuneInString(base)
		if unicode.IsLetter(char) {
			base = string(unicode.ToUpper(char))
			delete(modifiers, "shift")
		} else if index := strings.IndexRune("`1234567890-=[]\\;',./", char); index >= 0 {
			base = string([]rune("~!@#$%^&*()_+{}|:\"<>?")[index])
			delete(modifiers, "shift")
		}
	}

	var result []string
	for _, modifier := range []string{"ctrl", "alt", "shift", "super"} {
		if modifiers[modifier] {
			result = append(result, modifier)
		}
	}
	return strings.Join(append(result, base), "+")
}

func validKey(key string) bool {
	_, base := splitKey(key)
	if utf8.RuneCountInString(base) == 1 || slices.Contains([]string{
		"up", "down", "left", "right", "enter", "tab", "esc", "space", "backspace", "delete", "home", "end", "pgup", "pgdown",
	}, base) {
		return true
	}

	if strings.HasPrefix(base, "f") {
		number, err := strconv.Atoi(strings.TrimPrefix(base, "f"))
		return err == nil && number >= 1 && number <= 63
	}
	return false
}
