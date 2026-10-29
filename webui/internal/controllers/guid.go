// EmuStation Builder
// Copyright (C) 2026  Marcel Joachim Kloubert
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

// Package controllers handles SDL GUIDs, joystick discovery and the
// EmulationStation input configuration.
package controllers

import (
	"encoding/binary"
	"encoding/hex"
)

// CRC16 is SDL_crc16 (CRC-16/ARC: reflected polynomial 0xA001, init 0).
func CRC16(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// SDLGUID builds the 32 hex character SDL joystick GUID. With withCRC the
// CRC16 of the device name is stored in bytes 2-3, otherwise they are zero.
func SDLGUID(bus, vendor, product, version uint16, name string, withCRC bool) string {
	var g [16]byte
	le := binary.LittleEndian
	le.PutUint16(g[0:], bus)
	if withCRC {
		le.PutUint16(g[2:], CRC16([]byte(name)))
	}
	le.PutUint16(g[4:], vendor)
	le.PutUint16(g[8:], product)
	le.PutUint16(g[12:], version)
	return hex.EncodeToString(g[:])
}
