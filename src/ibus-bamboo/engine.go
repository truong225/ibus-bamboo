/*
 * Bamboo - A Vietnamese Input method editor
 * Copyright (C) 2018 Luong Thanh Lam <ltlam93@gmail.com>
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package main

import (
	"fmt"
	"github.com/BambooEngine/bamboo-core"
	"github.com/BambooEngine/goibus/ibus"
	"github.com/godbus/dbus"
	"os/exec"
	"sync"
)

type IBusBambooEngine struct {
	sync.Mutex
	ibus.Engine
	preediter           bamboo.IEngine
	zeroLocation        bool
	engineName          string
	config              *Config
	propList            *ibus.PropList
	mode                bamboo.Mode
	ignorePreedit       bool
	macroTable          *MacroTable
	dictionary          map[string]bool
	display             CDisplay
	wmClasses           []string
	lookupTableIsOpened bool
	capSurrounding      bool
	nBackSpace          int
	newChars            []rune
}

/**
Implement IBus.Engine's process_key_event default signal handler.

Args:
	keyval - The keycode, transformed through a keymap, stays the
		same for every keyboard
	keycode - Keyboard-dependant key code
	modifiers - The state of IBus.ModifierType keys like
		Shift, Control, etc.
Return:
	True - if successfully process the keyevent, it won't be sent to X-server
	False - otherwise.

This function gets called whenever a key is pressed.
*/
func (e *IBusBambooEngine) ProcessKeyEvent(keyVal uint32, keyCode uint32, state uint32) (bool, *dbus.Error) {
	e.Lock()
	defer e.Unlock()
	var rawKeyLen = e.getRawKeyLen()

	if e.zeroLocation || (!e.inLookupTableControlKeys(keyVal) && inWhiteList(e.config.ExceptWhiteList, e.wmClasses)) ||
		state&IBUS_RELEASE_MASK != 0 || //Ignore key-up event
		(state&IBUS_SHIFT_MASK == 0 && (keyVal == IBUS_Shift_L || keyVal == IBUS_Shift_R)) { //Ignore 1 shift key
		return false, nil
	}

	if state&IBUS_CONTROL_MASK != 0 ||
		state&IBUS_MOD1_MASK != 0 ||
		state&IBUS_IGNORED_MASK != 0 ||
		state&IBUS_SUPER_MASK != 0 ||
		state&IBUS_HYPER_MASK != 0 ||
		state&IBUS_META_MASK != 0 {
		e.ignorePreedit = false
		if rawKeyLen == 0 {
			return false, nil
		} else {
			//while typing, do not process control keys
			return true, nil
		}
	}
	fmt.Printf("keyCode 0x%04x keyval 0x%04x | %c\n", keyCode, keyVal, rune(keyVal))
	if keyVal == IBUS_OpenLookupTable && e.lookupTableIsOpened == false {
		e.preediter.Reset()
		e.lookupTableIsOpened = true
		e.openLookupTable()
		return true, nil
	}
	if e.lookupTableIsOpened {
		e.lookupTableIsOpened = false
		return e.ltProcessKeyEvent(keyVal, keyCode, state)
	}
	if e.inBackspaceWhiteList(e.wmClasses) {
		return e.backspaceProcessKeyEvent(keyVal, keyCode, state)
	}
	return e.preeditProcessKeyEvent(keyVal, keyCode, state)
}

func (e *IBusBambooEngine) FocusIn() *dbus.Error {
	e.Lock()
	defer e.Unlock()
	if e.display == nil {
		e.display = x11OpenDisplay()
	}
	var wmClasses []string
	if e.display != nil {
		wmClasses = x11GetFocusWindowClass(e.display)
		fmt.Println(e.wmClasses)
	}

	e.RegisterProperties(e.propList)
	e.HidePreeditText()
	if !isSameClasses(e.wmClasses, wmClasses) {
		e.preediter.Reset()
	}
	e.wmClasses = wmClasses
	fmt.Print("FocusIn.")

	return nil
}

func (e *IBusBambooEngine) FocusOut() *dbus.Error {
	fmt.Print("FocusOut.")
	//e.wmClasses = []string{}
	return nil
}

func (e *IBusBambooEngine) Reset() *dbus.Error {
	fmt.Print("Reset.")
	return nil
}

func (e *IBusBambooEngine) Enable() *dbus.Error {
	fmt.Print("Enable.")
	mouseCaptureInit()
	return nil
}

func (e *IBusBambooEngine) Disable() *dbus.Error {
	fmt.Print("Disable.")
	mouseCaptureExit()
	if e.display != nil {
		x11CloseDisplay(e.display)
		e.display = nil
	}
	return nil
}

func (e *IBusBambooEngine) SetCapabilities(cap uint32) *dbus.Error {
	e.capSurrounding = cap&IBUS_CAP_SURROUNDING_TEXT != 0
	return nil
}

func (e *IBusBambooEngine) SetCursorLocation(x int32, y int32, w int32, h int32) *dbus.Error {
	e.zeroLocation = x == 0 && y == 0 && w == 0 && h == 0
	return nil
}

func (e *IBusBambooEngine) SetContentType(purpose uint32, hints uint32) *dbus.Error {
	return nil
}

//@method(in_signature="su")
func (e *IBusBambooEngine) PropertyActivate(propName string, propState uint32) *dbus.Error {
	if propName == PropKeyAbout {
		exec.Command("xdg-open", HomePage).Start()
		return nil
	}
	if propName == PropKeyVnConvert {
		exec.Command("xdg-open", VnConvertPage).Start()
		return nil
	}
	if propName == PropKeyMacroTable {
		OpenMactabFile(EngineName)
	}

	turnSpellChecking := func(on bool) {
		if on {
			e.config.IBflags |= IBspellChecking
			e.config.IBflags |= IBautoNonVnRestore
			if e.config.IBflags&IBspellCheckingWithDicts == 0 {
				e.config.IBflags |= IBspellCheckingWithRules
			}
		} else {
			e.config.IBflags &= ^IBspellChecking
			e.config.IBflags &= ^IBautoNonVnRestore
			e.config.IBflags &= ^IBautoCommitWithVnNotMatch
			e.config.IBflags &= ^IBautoCommitWithVnFullMatch
		}
	}

	if propName == PropKeyStdToneStyle {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.Flags |= bamboo.EstdToneStyle
		} else {
			e.config.Flags &= ^bamboo.EstdToneStyle
		}
	}
	if propName == PropKeyFreeToneMarking {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.Flags |= bamboo.EfreeToneMarking
		} else {
			e.config.Flags &= ^bamboo.EfreeToneMarking
		}
	}
	if propName == PropKeySpellingChecking {
		if propState == ibus.PROP_STATE_CHECKED {
			turnSpellChecking(true)
		} else {
			turnSpellChecking(false)
		}
	}
	if propName == PropKeySpellCheckingByRules {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= IBspellCheckingWithRules
			turnSpellChecking(true)
		} else {
			e.config.IBflags &= ^IBspellCheckingWithRules
		}
	}
	if propName == PropKeySpellCheckingByDicts {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= IBspellCheckingWithDicts
			turnSpellChecking(true)
		} else {
			e.config.IBflags &= ^IBspellCheckingWithDicts
		}
	}
	if propName == PropKeyAutoCommitWithVnNotMatch {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= IBautoCommitWithVnNotMatch
		} else {
			e.config.IBflags &= ^IBautoCommitWithVnNotMatch
		}
	}
	if propName == PropKeyAutoCommitWithVnFullMatch {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= IBautoCommitWithVnFullMatch
		} else {
			e.config.IBflags &= ^IBautoCommitWithVnFullMatch
		}
	}
	if propName == PropKeyAutoCommitWithVnWordBreak {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= IBautoCommitWithVnWordBreak
		} else {
			e.config.IBflags &= ^IBautoCommitWithVnWordBreak
		}
	}
	if propName == PropKeyAutoCommitWithMouseMovement {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= IBautoCommitWithMouseMovement
		} else {
			e.config.IBflags &= ^IBautoCommitWithMouseMovement
		}
	}
	if propName == PropKeyAutoCommitWithDelay {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= IBautoCommitWithDelay
		} else {
			e.config.IBflags &= ^IBautoCommitWithDelay
		}
	}
	if propName == PropKeyMacroEnabled {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= IBmarcoEnabled
			e.config.IBflags &= ^IBautoCommitWithVnNotMatch
			e.config.IBflags &= ^IBautoCommitWithVnFullMatch
			e.config.IBflags &= ^IBautoCommitWithVnWordBreak
			e.macroTable.Enable()
		} else {
			e.config.IBflags &= ^IBmarcoEnabled
			e.macroTable.Disable()
		}
	}
	if propName == PropKeyPreeditInvisibility {
		if propState == ibus.PROP_STATE_CHECKED {
			e.config.IBflags |= IBpreeditInvisibility
		} else {
			e.config.IBflags &= ^IBpreeditInvisibility
		}
	}
	if isValidCharset(getCharsetFromPropKey(propName)) && propState == ibus.PROP_STATE_CHECKED {
		e.config.Charset = getCharsetFromPropKey(propName)
	}
	if _, found := bamboo.InputMethods[propName]; found && propState == ibus.PROP_STATE_CHECKED {
		e.config.InputMethod = propName
	}
	SaveConfig(e.config, e.engineName)
	e.propList = GetPropListByConfig(e.config)
	e.preediter = bamboo.NewEngine(e.config.InputMethod, e.config.Flags, e.dictionary)
	e.RegisterProperties(e.propList)
	return nil
}
