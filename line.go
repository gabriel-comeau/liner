// +build windows linux darwin openbsd freebsd netbsd

package liner

import (
	"container/ring"
	"errors"
	"fmt"
	"io"
	"regexp"
	"unicode"
	"unicode/utf8"
)

type action int

const (
	left action = iota
	right
	up
	down
	home
	end
	insert
	del
	pageUp
	pageDown
	f1
	f2
	f3
	f4
	f5
	f6
	f7
	f8
	f9
	f10
	f11
	f12
	altY
	shiftTab
	wordLeft
	wordRight
	winch
	unknown
)

const (
	ctrlA = 1
	ctrlB = 2
	ctrlC = 3
	ctrlD = 4
	ctrlE = 5
	ctrlF = 6
	ctrlG = 7
	ctrlH = 8
	tab   = 9
	lf    = 10
	ctrlK = 11
	ctrlL = 12
	cr    = 13
	ctrlN = 14
	ctrlO = 15
	ctrlP = 16
	ctrlQ = 17
	ctrlR = 18
	ctrlS = 19
	ctrlT = 20
	ctrlU = 21
	ctrlV = 22
	ctrlW = 23
	ctrlX = 24
	ctrlY = 25
	ctrlZ = 26
	esc   = 27
	bs    = 127
)

const (
	beep = "\a"
)

var (
	colorExpr *regexp.Regexp
)

func (s *State) refresh(prompt string, buf string, pos int) error {
	s.cursorPos(0)
	_, err := fmt.Print(prompt)
	if err != nil {
		return err
	}

	pLen := utf8.RuneCountInString(stripAnsiColorSequences(prompt))
	bLen := utf8.RuneCountInString(buf)
	if pLen+bLen < s.columns {
		_, err = fmt.Print(buf)
		s.eraseLine()
		s.cursorPos(pLen + pos)
	} else {
		// Find space available
		space := s.columns - pLen
		space-- // space for cursor
		start := pos - space/2
		end := start + space
		if end > bLen {
			end = bLen
			start = end - space
		}
		if start < 0 {
			start = 0
			end = space
		}
		pos -= start

		// Leave space for markers
		if start > 0 {
			start++
		}
		if end < bLen {
			end--
		}
		line := []rune(buf)
		line = line[start:end]

		// Output
		if start > 0 {
			fmt.Print("{")
		}
		fmt.Print(string(line))
		if end < bLen {
			fmt.Print("}")
		}

		// Set cursor position
		s.eraseLine()
		s.cursorPos(pLen + pos)
	}
	return err
}

func (s *State) tabComplete(p string, line []rune, pos int) ([]rune, int, interface{}, error) {
	if s.completer == nil {
		return line, pos, rune(tab), nil
	}
	head, list, tail := s.completer(string(line), pos)
	if len(list) <= 0 {
		return line, pos, rune(tab), nil
	}
	listEntry := 0
	hl := utf8.RuneCountInString(head)
	for {
		pick := list[listEntry]
		s.refresh(p, head+pick+tail, hl+utf8.RuneCountInString(pick))

		next, err := s.readNext()
		if err != nil {
			return line, pos, rune(tab), err
		}
		if key, ok := next.(rune); ok {
			if key == tab {
				if listEntry < len(list)-1 {
					listEntry++
				} else {
					listEntry = 0
				}
				continue
			}
			if key == esc {
				return line, pos, rune(esc), nil
			}
		}
		if a, ok := next.(action); ok && a == shiftTab {
			if listEntry > 0 {
				listEntry--
			} else {
				listEntry = len(list) - 1
			}
			continue
		}
		return []rune(head + pick + tail), hl + utf8.RuneCountInString(pick), next, nil
	}
	// Not reached
	return line, pos, rune(tab), nil
}

// reverse intelligent search, implements a bash-like history search.
func (s *State) reverseISearch(origLine []rune, origPos int) ([]rune, int, interface{}, error) {
	p := "(reverse-i-search)`': "
	s.refresh(p, string(origLine), origPos)

	line := []rune{}
	pos := 0
	var foundLine string
	var foundPos int

	getLine := func() (string, string, int) {
		search := string(line)
		prompt := "(reverse-i-search)`%s': "
		return fmt.Sprintf(prompt, search), foundLine, foundPos
	}

	history, positions := s.getHistoryByPattern(string(line))
	historyPos := len(history) - 1

	for {
		next, err := s.readNext()
		if err != nil {
			return []rune(foundLine), foundPos, esc, err
		}

		switch v := next.(type) {
		case rune:
			switch v {
			case ctrlR: // Search backwards
				if historyPos > 0 && historyPos < len(history) {
					historyPos--
					foundLine = history[historyPos]
					foundPos = positions[historyPos]
				} else {
					fmt.Print(beep)
				}
			case ctrlS: // Search forward
				if historyPos < len(history)-1 && historyPos >= 0 {
					historyPos++
					foundLine = history[historyPos]
					foundPos = positions[historyPos]
				} else {
					fmt.Print(beep)
				}
			case ctrlH, bs: // Backspace
				if pos <= 0 {
					fmt.Print(beep)
				} else {
					line = append(line[:pos-1], line[pos:]...)
					pos--

					// For each char deleted, display the last matching line of history
					history, positions := s.getHistoryByPattern(string(line))
					historyPos = len(history) - 1
					if len(history) > 0 {
						foundLine = history[historyPos]
						foundPos = positions[historyPos]
					} else {
						foundLine = ""
						foundPos = 0
					}
				}
			case ctrlG: // Cancel
				return origLine, origPos, esc, err

			case tab, cr, lf, ctrlA, ctrlB, ctrlD, ctrlE, ctrlF, ctrlK,
				ctrlL, ctrlN, ctrlO, ctrlP, ctrlQ, ctrlT, ctrlU, ctrlV, ctrlW, ctrlX, ctrlY, ctrlZ:
				fallthrough
			case 0, ctrlC, esc, 28, 29, 30, 31:
				return []rune(foundLine), foundPos, next, err
			default:
				line = append(line[:pos], append([]rune{v}, line[pos:]...)...)
				pos++

				// For each keystroke typed, display the last matching line of history
				history, positions = s.getHistoryByPattern(string(line))
				historyPos = len(history) - 1
				if len(history) > 0 {
					foundLine = history[historyPos]
					foundPos = positions[historyPos]
				} else {
					foundLine = ""
					foundPos = 0
				}
			}
		case action:
			return []rune(foundLine), foundPos, next, err
		}
		s.refresh(getLine())
	}
}

// addToKillRing adds some text to the kill ring. If mode is 0 it adds it to a
// new node in the end of the kill ring, and move the current pointer to the new
// node. If mode is 1 or 2 it appends or prepends the text to the current entry
// of the killRing.
func (s *State) addToKillRing(text []rune, mode int) {
	// Don't use the same underlying array as text
	killLine := make([]rune, len(text))
	copy(killLine, text)

	// Point killRing to a newNode, procedure depends on the killring state and
	// append mode.
	if mode == 0 { // Add new node to killRing
		if s.killRing == nil { // if killring is empty, create a new one
			s.killRing = ring.New(1)
		} else if s.killRing.Len() >= KillRingMax { // if killring is "full"
			s.killRing = s.killRing.Next()
		} else { // Normal case
			s.killRing.Link(ring.New(1))
			s.killRing = s.killRing.Next()
		}
	} else {
		if s.killRing == nil { // if killring is empty, create a new one
			s.killRing = ring.New(1)
			s.killRing.Value = []rune{}
		}
		if mode == 1 { // Append to last entry
			killLine = append(s.killRing.Value.([]rune), killLine...)
		} else if mode == 2 { // Prepend to last entry
			killLine = append(killLine, s.killRing.Value.([]rune)...)
		}
	}

	// Save text in the current killring node
	s.killRing.Value = killLine
}

func (s *State) yank(p string, text []rune, pos int) ([]rune, int, interface{}, error) {
	lineStart := text[:pos]
	lineEnd := text[pos:]
	var line []rune

	for {
		value := s.killRing.Value.([]rune)
		line = make([]rune, 0)
		line = append(line, lineStart...)
		line = append(line, value...)
		line = append(line, lineEnd...)

		pos = len(lineStart) + len(value)
		s.refresh(p, string(line), pos)

		next, err := s.readNext()
		if err != nil {
			return line, pos, next, err
		}

		switch v := next.(type) {
		case rune:
			return line, pos, next, nil
		case action:
			switch v {
			case altY:
				s.killRing = s.killRing.Prev()
			default:
				return line, pos, next, nil
			}
		}
	}

	return line, pos, esc, nil
}

// Prompt displays p, and then waits for user input. Prompt allows line editing
// if the terminal supports it.
func (s *State) Prompt(p string) (string, error) {
	if !s.terminalOutput {
		return "", errNotTerminalOutput
	}
	if !s.terminalSupported {
		return s.promptUnsupported(p)
	}

	s.historyMutex.RLock()
	defer s.historyMutex.RUnlock()

	s.startPrompt()
	s.getColumns()

	fmt.Print(p)
	var line []rune
	pos := 0
	var historyEnd string
	prefixHistory := s.getHistoryByPrefix(string(line))
	historyPos := len(prefixHistory)
	var historyAction bool // used to mark history related actions
	var killAction int = 0 // used to mark kill related actions
mainLoop:
	for {
		historyAction = false
		next, err := s.readNext()
		if err != nil {
			return "", err
		}

		// If the key is a tab do autocomplete, and then resume execution as usual
		if key, ok := next.(rune); ok && key == tab {
			line, pos, next, err = s.tabComplete(p, line, pos)
			if err != nil {
				return "", err
			}
			s.refresh(p, string(line), pos)
		}

		// If the key is a CtrlY && killring is not empty do yank. If yank returns
		// next == ctrlY keep looping. Then resume normal execution
		cont := true
		for cont {
			if key, ok := next.(rune); ok && key == ctrlY && s.killRing != nil {
				line, pos, next, err = s.yank(p, line, pos)
				if err != nil {
					return "", err
				}
				if key, ok := next.(rune); !ok || key != ctrlY {
					cont = false
				}
			} else {
				cont = false
			}
		}

		// If the key is a CtrlR do reverse intelligent search, then resume execution
		if key, ok := next.(rune); ok && key == ctrlR {
			line, pos, next, err = s.reverseISearch(line, pos)
			if err != nil {
				return "", err
			}
			s.refresh(p, string(line), pos)
		}

		switch v := next.(type) {
		case rune:
			switch v {
			case cr, lf:
				fmt.Println()
				break mainLoop
			case ctrlA: // Start of line
				pos = 0
				s.refresh(p, string(line), pos)
			case ctrlE: // End of line
				pos = len(line)
				s.refresh(p, string(line), pos)
			case ctrlB: // left
				if pos > 0 {
					pos--
					s.refresh(p, string(line), pos)
				} else {
					fmt.Print(beep)
				}
			case ctrlF: // right
				if pos < len(line) {
					pos++
					s.refresh(p, string(line), pos)
				} else {
					fmt.Print(beep)
				}
			case ctrlD: // del
				if pos == 0 && len(line) == 0 {
					// exit
					return "", io.EOF
				}

				// ctrlD is a potential EOF, so the rune reader shuts down.
				// Therefore, if it isn't actually an EOF, we must re-startPrompt.
				s.startPrompt()

				if pos >= len(line) {
					fmt.Print(beep)
				} else {
					line = append(line[:pos], line[pos+1:]...)
					s.refresh(p, string(line), pos)
				}
			case ctrlK: // delete remainder of line
				if pos >= len(line) {
					fmt.Print(beep)
				} else {
					if killAction > 0 {
						s.addToKillRing(line[pos:], 1) // Add in apend mode
					} else {
						s.addToKillRing(line[pos:], 0) // Add in normal mode
					}

					killAction = 2 // Mark that there was a kill action
					line = line[:pos]
					s.refresh(p, string(line), pos)
				}
			case ctrlP: // up
				historyAction = true
				if historyPos > 0 {
					if historyPos == len(prefixHistory) {
						historyEnd = string(line)
					}
					historyPos--
					line = []rune(prefixHistory[historyPos])
					pos = len(line)
					s.refresh(p, string(line), pos)
				} else {
					fmt.Print(beep)
				}
			case ctrlN: // down
				historyAction = true
				if historyPos < len(prefixHistory) {
					historyPos++
					if historyPos == len(prefixHistory) {
						line = []rune(historyEnd)
					} else {
						line = []rune(prefixHistory[historyPos])
					}
					pos = len(line)
					s.refresh(p, string(line), pos)
				} else {
					fmt.Print(beep)
				}
			case ctrlT: // transpose prev rune with rune under cursor
				if len(line) < 2 || pos < 1 {
					fmt.Print(beep)
				} else {
					if pos == len(line) {
						pos--
					}
					line[pos-1], line[pos] = line[pos], line[pos-1]
					pos++
					s.refresh(p, string(line), pos)
				}
			case ctrlL: // clear screen
				s.eraseScreen()
				s.refresh(p, string(line), pos)
			case ctrlH, bs: // Backspace
				if pos <= 0 {
					fmt.Print(beep)
				} else {
					line = append(line[:pos-1], line[pos:]...)
					pos--
					s.refresh(p, string(line), pos)
				}
			case ctrlU: // Erase line before cursor
				if killAction > 0 {
					s.addToKillRing(line[:pos], 2) // Add in prepend mode
				} else {
					s.addToKillRing(line[:pos], 0) // Add in normal mode
				}

				killAction = 2 // Mark that there was some killing
				line = line[pos:]
				pos = 0
				s.refresh(p, string(line), pos)
			case ctrlW: // Erase word
				if pos == 0 {
					fmt.Print(beep)
					break
				}
				// Remove whitespace to the left
				buf := make([]rune, 0) // Store the deleted chars in a buffer
				for {
					if pos == 0 || !unicode.IsSpace(line[pos-1]) {
						break
					}
					buf = append(buf, line[pos-1])
					line = append(line[:pos-1], line[pos:]...)
					pos--
				}
				// Remove non-whitespace to the left
				for {
					if pos == 0 || unicode.IsSpace(line[pos-1]) {
						break
					}
					buf = append(buf, line[pos-1])
					line = append(line[:pos-1], line[pos:]...)
					pos--
				}
				// Invert the buffer and save the result on the killRing
				newBuf := make([]rune, 0)
				for i := len(buf) - 1; i >= 0; i-- {
					newBuf = append(newBuf, buf[i])
				}
				if killAction > 0 {
					s.addToKillRing(newBuf, 2) // Add in prepend mode
				} else {
					s.addToKillRing(newBuf, 0) // Add in normal mode
				}
				killAction = 2 // Mark that there was some killing

				s.refresh(p, string(line), pos)
			// Catch keys that do nothing, but you don't want them to beep
			case esc:
				// DO NOTHING
			// Catch keys that are handled before the switch
			case tab, ctrlR, ctrlY:
				fallthrough
			// Unused keys
			case ctrlG, ctrlO, ctrlQ, ctrlS, ctrlV, ctrlX, ctrlZ:
				fallthrough
			// Catch unhandled control codes (anything <= 31)
			case 0, ctrlC, 28, 29, 30, 31:
				fmt.Print(beep)
			default:
				if pos == len(line) && len(p)+len(line) < s.columns-1 {
					line = append(line, v)
					fmt.Printf("%c", v)
					pos++
				} else {
					line = append(line[:pos], append([]rune{v}, line[pos:]...)...)
					pos++
					s.refresh(p, string(line), pos)
				}
			}
		case action:
			switch v {
			case del:
				if pos >= len(line) {
					fmt.Print(beep)
				} else {
					line = append(line[:pos], line[pos+1:]...)
				}
			case left:
				if pos > 0 {
					pos--
				} else {
					fmt.Print(beep)
				}
			case wordLeft:
				if pos > 0 {
					for {
						pos--
						if pos == 0 || unicode.IsSpace(line[pos-1]) {
							break
						}
					}
				} else {
					fmt.Print(beep)
				}
			case right:
				if pos < len(line) {
					pos++
				} else {
					fmt.Print(beep)
				}
			case wordRight:
				if pos < len(line) {
					for {
						pos++
						if pos == len(line) || unicode.IsSpace(line[pos]) {
							break
						}
					}
				} else {
					fmt.Print(beep)
				}
			case up:
				historyAction = true
				if historyPos > 0 {
					if historyPos == len(prefixHistory) {
						historyEnd = string(line)
					}
					historyPos--
					line = []rune(prefixHistory[historyPos])
					pos = len(line)
				} else {
					fmt.Print(beep)
				}
			case down:
				historyAction = true
				if historyPos < len(prefixHistory) {
					historyPos++
					if historyPos == len(prefixHistory) {
						line = []rune(historyEnd)
					} else {
						line = []rune(prefixHistory[historyPos])
					}
					pos = len(line)
				} else {
					fmt.Print(beep)
				}
			case home: // Start of line
				pos = 0
			case end: // End of line
				pos = len(line)
			}
			s.refresh(p, string(line), pos)
		}
		if !historyAction {
			prefixHistory = s.getHistoryByPrefix(string(line))
			historyPos = len(prefixHistory)
		}
		if killAction > 0 {
			killAction--
		}
	}
	return string(line), nil
}

// PasswordPrompt displays p, and then waits for user input. The input typed by
// the user is not displayed in the terminal.
func (s *State) PasswordPrompt(p string) (string, error) {
	if !s.terminalOutput {
		return "", errNotTerminalOutput
	}
	if !s.terminalSupported {
		return "", errors.New("liner: function not supported in this terminal")
	}

	s.startPrompt()
	s.getColumns()

	fmt.Print(p)
	var line []rune
	pos := 0

mainLoop:
	for {
		next, err := s.readNext()
		if err != nil {
			return "", err
		}

		switch v := next.(type) {
		case rune:
			switch v {
			case cr, lf:
				fmt.Println()
				break mainLoop
			case ctrlD: // del
				if pos == 0 && len(line) == 0 {
					// exit
					return "", io.EOF
				}

				// ctrlD is a potential EOF, so the rune reader shuts down.
				// Therefore, if it isn't actually an EOF, we must re-startPrompt.
				s.startPrompt()
			case ctrlL: // clear screen
				s.eraseScreen()
				s.refresh(p, "", 0)
			case ctrlH, bs: // Backspace
				if pos <= 0 {
					fmt.Print(beep)
				} else {
					line = append(line[:pos-1], line[pos:]...)
					pos--
				}
			// Unused keys
			case esc, tab, ctrlA, ctrlB, ctrlE, ctrlF, ctrlG, ctrlK, ctrlN, ctrlO, ctrlP, ctrlQ, ctrlR, ctrlS,
				ctrlT, ctrlU, ctrlV, ctrlW, ctrlX, ctrlY, ctrlZ:
				fallthrough
			// Catch unhandled control codes (anything <= 31)
			case 0, ctrlC, 28, 29, 30, 31:
				fmt.Print(beep)
			default:
				line = append(line[:pos], append([]rune{v}, line[pos:]...)...)
				pos++
			}
		}
	}
	return string(line), nil
}

// Remove any ansi color sequences from a string, used for calculating
// the length of the prompt.
func stripAnsiColorSequences(in string) string {
	if colorExpr == nil {
		colorExpr = regexp.MustCompile("\x1b[^m]*m")
	}

	return colorExpr.ReplaceAllString(in, "")
}
