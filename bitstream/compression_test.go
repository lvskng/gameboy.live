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
	drawFunc := func(pixel byte, x, y int) {
		lastBitmap[x][y] = pixel
	}
	data := difbmp
	DecompressLine(&data, drawFunc)
	var pixels [][4]byte
	var lines []int
	var printed bool
	for i, line := range lastBitmap {
		if line != orbitmap[i] {
			for index, pixel := range line {
				if pixel != orbitmap[i][index] { //line, pos, expected, is
					pixels = append(pixels, [4]byte{byte(i), byte(index), orbitmap[i][index], pixel})
					log.Printf("Validation failed for line %d, position %d: expected %d got %d", i, index, orbitmap[i][index], pixel)
					if !printed {
						log.Printf("Line: %X", line)
					}
					printed = true
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
		log.Printf("difstrings for invalid lines: %X", d)
	}
	return len(pixels) == 0
}
