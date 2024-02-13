package bitstream

import (
	"log"
	"math/rand"
	"sync"
	"testing"
)

func TestDifCompression(t *testing.T) {
	//Generate 160,144 array of random numbers between 0 and 3
	screen1 := generateRandomScreen()
	emptyScreen := [160][144]byte{}

	mockServer := Server{}

	mockServer.pixelLock = sync.RWMutex{}

	mockServer.pixels = &screen1

	bitmap1, orbitmap1 := mockServer.GetBitmapDelta(emptyScreen)

	if !validateDif(flattenBitmap(bitmap1), emptyScreen, orbitmap1) {
		t.Errorf("Failed to validate bitmap (empty -> random)")
	}

	var newScreen [160][144]byte
	for i := 0; i < 1000; i++ {
		oldScreen := newScreen
		newScreen = generateRandomScreen()

		mockServer.pixels = &newScreen

		newBitmap, newOrbitmap := mockServer.GetBitmapDelta(oldScreen)

		if !validateDif(flattenBitmap(newBitmap), oldScreen, newOrbitmap) {
			t.Errorf("Failed to validate bitmap (random -> random)")
			t.Fail()
		}
	}
}

func generateRandomScreen() [160][144]byte {
	screen := [160][144]byte{}
	for i := 0; i < 160; i++ {
		for j := 0; j < 144; j++ {
			screen[i][j] = byte(rand.Intn(3))
		}
	}
	return screen
}

func flattenBitmap(b [][]byte) []byte {
	msg := []byte{0xFB}
	for _, line := range b {
		if line[1] != 0xFE {
			msg = append(msg, line...)
		}
	}
	return msg
}

// validates a delta screen for debug purposes
func validateDif(difbmp []byte, lastBitmap, orbitmap [160][144]byte) bool {
	shift := func(slc []byte) (byte, []byte) {
		if len(slc) == 1 {
			return slc[0], []byte{}
		}
		return slc[0], slc[1:]
	}
	data := difbmp
	var b byte
	b, data = shift(data)
	if b != 0xFB {
		return false
	}
	x := 0
	y := 0
	for len(data) > 0 {
		var op byte
		op, data = shift(data)
		if op > 0x03 && op < 0xA5 {
			x = int(op - 4)
			y = 0
		} else if op == 0xF0 {
			for len(data) > 0 && (data[0] < 0x04 || data[0] == 0xFF) {
				var pixel byte
				pixel, data = shift(data)
				if pixel != 0xFF {
					lastBitmap[x][y] = pixel
				}
				y++
			}
		} else if op == 0xF1 {
			for len(data) > 0 && (data[0] < 0x04 || data[0] > 0xA4) {
				b, data = shift(data)
				if b < 0x04 {
					lastBitmap[x][y] = b
					y++
				} else if b == 0xFF {
					y++
				} else if b == 0xF2 {
					var rcount byte
					var rpx byte
					rcount, data = shift(data)
					rpx, data = shift(data)
					for i := 0; i <= int(rcount); i++ {
						if rpx != 0xFF {
							lastBitmap[x][y] = rpx
						}
						y++
					}
				} else if b > 0xD0 && b < 0xE0 {
					var rpx byte
					rcount := b - 0xD0
					rpx, data = shift(data)
					for i := 0; i <= int(rcount); i++ {
						if rpx != 0xFF {
							lastBitmap[x][y] = rpx
						}
						y++
					}
				} else if b == 0xFD {
					var rpx byte
					rpx, data = shift(data)
					for ; y < 144; y++ {
						if rpx != 0xFF {
							lastBitmap[x][y] = rpx
						}
					}
				}
			}
		} else if op == 0xFE {
			continue
		}
	}
	var pixels [][4]byte
	var lines []int
	for i, line := range lastBitmap {
		if line != orbitmap[i] {
			for index, pixel := range line {
				if pixel != orbitmap[i][index] { //line, pos, expected, is
					pixels = append(pixels, [4]byte{byte(i), byte(index), orbitmap[i][index], pixel})
					log.Printf("Validation failed for line %d, position %d: expected %d got %d", i, index, orbitmap[i][index], pixel)
				}
			}
			lines = append(lines, i)
		}
	}
	var difstrings [][]byte
	for _, linenum := range lines {
		record := false
		var lastb byte
		var ds []byte
		for _, b := range difbmp {
			if b == byte(linenum+0x04) {
				record = true
			} else if (b > 0x03 && b < 0xA5) && (lastb != 0xF2) {
				record = false
			}
			if record {
				ds = append(ds, b)
				lastb = b
			}
		}
		if len(ds) > 0 {
			difstrings = append(difstrings, ds)
		}
	}
	log.Printf("Validation failed for %d lines", len(difstrings))
	for _, d := range difstrings {
		log.Printf("difstrings for invalid lines: %+v", d)
	}
	return len(pixels) == 0
}

func validateLine(line []byte, lastLine [160]byte, originalLine [160]byte) (bool, [][3]int) {
	if decompressLine(line, lastLine) != originalLine {
		var pixels [][3]int
		for n, p := range line {
			if lastLine[n] != p { //pos ex act
				pixels = append(pixels, [3]int{n, int(p), int(lastLine[n])})
			}
		}
		return false, pixels
	}
	return true, nil
}

// There seems to be an issue with the F0 decoding. Some F0 lines fail validation.
// This issue might very well be in the compression step as well
func decompressLine(line []byte, lastLine [160]byte) [160]byte {
	//line compression validation for debug purposes
	shift := func(slc []byte) (byte, []byte) {
		if len(slc) == 1 {
			return slc[0], []byte{}
		}
		return slc[0], slc[1:]
	}
	data := line
	var b byte
	if b, data = shift(data); b != 0xFB {
		//nop
	}
	y := 0
	for len(data) > 0 {
		var op byte
		op, data = shift(data)
		if op > 0x03 && op < 0xA5 {
			y = 0
		} else if op == 0xF0 {
			for len(data) > 0 && (data[0] < 0x04 || data[0] == 0xFF) {
				var pixel byte
				pixel, data = shift(data)
				if pixel != 0xFF {
					lastLine[y] = pixel
				}
				y++
			}
		} else if op == 0xF1 {
			for len(data) > 0 && (data[0] < 0x04 || data[0] > 0xA4) {
				b, data = shift(data)
				if b < 0x04 {
					lastLine[y] = b
					y++
				} else if b == 0xFF {
					y++
				} else if b == 0xF2 {
					var rcount byte
					var rpx byte
					rcount, data = shift(data)
					rpx, data = shift(data)
					for i := 0; i <= int(rcount); i++ {
						if rpx != 0xFF {
							lastLine[y] = rpx
						}
						y++
					}
				} else if b > 0xC0 && b < 0xD0 {
					var rpx byte
					rcount := b - 0xC0
					rpx, data = shift(data)
					for i := 0; i <= int(rcount); i++ {
						if rpx != 0xFF {
							lastLine[y] = rpx
						}
						y++
					}
				} else if b == 0xFD {
					var rpx byte
					rpx, data = shift(data)
					for ; y < 144; y++ {
						if rpx != 0xFF {
							lastLine[y] = rpx
						}
					}
				}
			}
		} else if op == 0xFE {
			continue
		}
	}
	return lastLine
}
