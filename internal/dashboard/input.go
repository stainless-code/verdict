package dashboard

// consoleEvent is the part of one Windows console input record the read tick
// needs. It lives outside the Windows file so the rule below is tested on
// every platform.
type consoleEvent struct {
	kind    uint16
	char    uint16
	keyDown bool
}

// consoleKeyEvent is KEY_EVENT, the one record kind that can carry a character.
const consoleKeyEvent = 0x0001

// keyInput reports whether any record is a key press that yields a
// character. Focus, mouse, menu, and buffer-size records, key releases, and a
// bare modifier press (character 0) give a read nothing to return.
func keyInput(events []consoleEvent) bool {
	for _, e := range events {
		if e.kind == consoleKeyEvent && e.keyDown && e.char != 0 {
			return true
		}
	}
	return false
}
